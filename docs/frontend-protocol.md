# Front-end protocol (v1)

The contract between the **main site** (trusted, `MAIN_ORIGIN`), the
**user-content service** (untrusted content, `USERCONTENT_ORIGIN`) and a
**front end** (code running in the sandboxed iframe). Design rationale:
[frontends.md](frontends.md).

Local dev: main site `http://localhost:8080`, user-content
`http://127.0.0.1:8081`. Different hostnames, so they are different sites.

## Refs and files

- A **ref** names a front end. Today only built-ins exist: `builtin/<name>`,
  validated by `internal/frontend.Valid`. `builtin/site` is the default and
  fallback (`frontend.DefaultRef`).
- Files for a ref live at `<FRONTENDS_DIR>/<ref>/` (dev: `build/frontends/`),
  e.g. `build/frontends/builtin/particle-stream/index.html`. Cloud Storage
  replaces this later behind the same layout.
- Front ends use **relative** asset paths (trunk `--public-url ./`).

## Tokens and URLs

- Token: `internal/fetoken.Sign(key, {Ref, Issued})`, HMAC-SHA256 with
  `FRONTEND_SIGNING_KEY`. Path-safe.
- URL: `USERCONTENT_ORIGIN/t/<token>/` (the index) and
  `USERCONTENT_ORIGIN/t/<token>/<path>` (assets). `/t/<token>/` and
  `/t/<token>/index.html` both mean the index.

## User-content service

`cmd/usercontent`, a separate binary. Routes:

| Route | Behavior |
|---|---|
| `GET /t/{token}/{path...}` | verify the token → valid ref → serve `<ref>/<path>` |
| `GET /site-host.js` | the host API script (`web/usercontent`), with `__MAIN_ORIGIN__` replaced |
| `GET /healthz` | `ok` |
| anything else | 404 |

Rules for `/t/`:

- Bad or forged token, or invalid ref → **403**.
- The index needs token age ≤ **60s** *and* `Sec-Fetch-Dest: iframe`;
  otherwise 403. A missing header counts as a failure.
- Other files need token age ≤ **30 min**; a token issued in the future
  (beyond 30s of clock skew) → 403.
- Path cleaning: no `..`, no dotfiles, no directory listings. Missing file →
  404.

Headers on every `/t/` response:

```
Content-Security-Policy: sandbox allow-scripts; default-src 'self';
  script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline';
  img-src 'self' data: blob:; font-src 'self' data:; media-src 'self' blob:;
  connect-src 'self'; worker-src 'self' blob:; frame-src 'none'; form-action 'none';
  base-uri 'none'; frame-ancestors MAIN_ORIGIN
Access-Control-Allow-Origin: *
Cross-Origin-Resource-Policy: cross-origin
Referrer-Policy: no-referrer
X-Content-Type-Options: nosniff
Cache-Control: no-store                  (index)
Cache-Control: private, max-age=1800     (assets)
```

`'unsafe-inline'` is deliberate. The whole document is untrusted, so the
CSP's job is to stop network egress, not XSS. `/site-host.js` gets the same
CORS/CORP/nosniff headers and `Cache-Control: public, max-age=300`.

## Main site

- `GET /api/frontend`, JSON, `Cache-Control: no-store`:
  - Returns `{"ref": "...", "url": "<freshly signed index URL>"}`.
  - The ref is the visitor's per-visit pick: the `fe_pick` session cookie
    (`HttpOnly`, `SameSite=Lax`, no `Max-Age`). If there's no valid pick, it
    chooses at random from the approved rotation and sets the cookie.
  - `?fallback=1` always returns `frontend.DefaultRef` and doesn't change the
    cookie.
- Approved rotation, static for now: `builtin/site`, `builtin/particle-stream`.
  The `FRONTEND_ROTATION` env var (comma-separated refs, each validated) overrides
  it; the end-to-end tests use it to force a specific front end.
- The public shell no longer loads the wasm site directly (`/site/` and
  `SITE_DIR` go away); the site is `builtin/site` in the iframe.

### Parent page behavior (`web/static/frontend-host.js`)

1. On load, set `html.fe-loading`: transcript visually hidden (still in the
   accessibility tree), plain background.
2. Fetch `/api/frontend` and create a full-viewport
   `<iframe sandbox="allow-scripts" allow="fullscreen" src=url title="Site">`.
   The iframe gets `aria-hidden="true"`; the transcript is the accessible
   content.
3. Accept messages **only** where `event.source === iframe.contentWindow`.
4. On `site:hello`, reply `site:init` with the content and the route.
5. On `site:ready`, switch to `html.fe-live`.
6. Failure means any of these:
   - no `site:ready` within **10s**;
   - a `site:error` before ready;
   - a `site:error` with kind `gpu-lost` at any time.

   On failure: if the current ref isn't the default, replace the iframe with
   `/api/frontend?fallback=1`. If the default fails too, remove the iframe and
   drop both classes, so the transcript shows as a plain HTML site.
7. **Navigation:**
   - On `site:navigate {slug}`: `history.pushState` to `/<slug>` (or `/`),
     fetch that URL, swap in the transcript's `<main>` and `<title>` from the
     response, and post `site:route`.
   - `popstate` does the same, without pushing.
   - Unknown slugs: let the fetch 404 and show the 404 transcript.

Content payload (the same shape as `/api/site.json`):

```json
{ "pages": [{"slug": "", "title": "...", "body": "..."}],
  "experiments": [{"slug": "...", "title": "...", "summary": "..."}] }
```

## Messages (postMessage)

Every message is `{ "v": 1, "type": "...", ...fields }`.

- **The parent** posts to the iframe with target origin `"*"`, because the
  iframe's origin is opaque. The content is public, so that's safe.
- **The iframe** posts to the parent with target origin `MAIN_ORIGIN`. It
  accepts only messages where `event.source === window.parent` and
  `event.origin === MAIN_ORIGIN`.

| Direction | type | fields |
|---|---|---|
| iframe → parent | `site:hello` | none (sent as soon as site-host.js runs) |
| parent → iframe | `site:init` | `content`, `route` (slug string, `""` = home) |
| iframe → parent | `site:ready` | none |
| iframe → parent | `site:navigate` | `slug` |
| parent → iframe | `site:route` | `route` |
| iframe → parent | `site:error` | `kind` (`error`, `unhandledrejection`, `gpu-lost`, `report`), `message`, `stack?` |

## Host API (`/site-host.js`, inside the iframe)

A classic script (not a module). It defines `window.site`:

| Member | |
|---|---|
| `site.loaded` | Promise resolved with `{content, route}` when `site:init` arrives |
| `site.content`, `site.route` | set on init; `route` updates on `site:route` |
| `site.onRoute(fn)` | subscribe to route changes; returns an unsubscribe function |
| `site.navigate(slug)` | ask the parent to navigate |
| `site.ready()` | signal the first frame is up (idempotent) |
| `site.reportError(err, kind = "report")` | report an error |
| `site.textCanvas(text, {font, color, maxWidth, lineHeight, padding})` | draws text into an `OffscreenCanvas` (a 2D canvas if unavailable) and returns it, ready for `copyExternalImageToTexture` |

It also forwards `window` `error` and `unhandledrejection` events as
`site:error`. Front ends call `site.reportError(e, "gpu-lost")` from their
`device.lost` handler.

Front ends run in an opaque origin: `localStorage`, `sessionStorage`,
IndexedDB and cookies throw or are unavailable. Wrap any use in try/catch.
