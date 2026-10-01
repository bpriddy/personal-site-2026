# Front-end protocol (v1)

The contract between the **main site** (trusted, `MAIN_ORIGIN`), the
**user-content service** (untrusted content, `USERCONTENT_ORIGIN`) and a
**front end** (code running in the sandboxed iframe). Design rationale:
[frontends.md](frontends.md).

Local dev: main site `http://localhost:8090`, user-content
`http://127.0.0.1:8091`. Different hostnames, so they are different sites.

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
| `GET /health` | `ok` (not `/healthz`: Cloud Run reserves paths ending in `z`) |
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
- Approved rotation: rows in the `frontends` table with `in_rotation = true`
  (toggled in `/admin/`); `builtin/site` sorts first. If none are in rotation,
  or the database errors, visitors get `builtin/site`.
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
6. If no front end is ready within **1.5s**, drop `fe-loading` so the transcript
   shows while the front end keeps loading; `site:ready` still switches to
   `fe-live`. Any failure also shows the transcript immediately.
7. Failure means any of these:
   - no `site:ready` within **10s**;
   - a `site:error` before ready;
   - a `site:error` with kind `gpu-lost` at any time.

   On failure: if the current ref isn't the default, replace the iframe with
   `/api/frontend?fallback=1`. If the default fails too, remove the iframe and
   drop both classes, so the transcript shows as a plain HTML site.
8. **Navigation:**
   - On `site:navigate {slug}`: `history.pushState` to `/<slug>` (or `/`),
     fetch that URL, swap in the transcript's `<main>` and `<title>` from the
     response, and post `site:route`.
   - `popstate` does the same, without pushing.
   - Unknown slugs: let the fetch 404 and show the 404 transcript.

Content payload (the same shape as `/api/site.json`; since v1.1 this is the
content contract, see "Content contract" below):

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

---

# v1.1 additions (2026-09-30): contract, builder, observer

Design: [observer.md](observer.md). Everything below is additive to v1.

## Front ends vs. revisions (builder)

- A **front end** has a stable id, and that id is what the rotation and the
  `frontends` table hold:
  - built-ins: `builtin/<name>`, as now;
  - prompted: `fe/<slug>` (`[a-z0-9][a-z0-9-]{0,62}`).
- A prompted front end is a series of immutable **revisions**. The one in the
  rotation is its **active revision**.
- **Servable refs** are what tokens carry and what the user-content service
  maps to files:
  - `builtin/<name>` → `<FRONTENDS_DIR>/builtin/<name>/`;
  - `rev/<id>`, where `<id>` is `[a-z0-9]{8,40}` → `<FRONTENDS_DIR>/rev/<id>/`
    locally, or the GCS objects `rev/<id>/...` in `FRONTENDS_BUCKET`.
- `frontend.Valid` accepts both servable forms. `frontend.ValidID` accepts
  `builtin/*` and `fe/*`.
- `GET /api/frontend` returns
  `{"ref": "<frontend id>", "serve": "<servable ref>", "url": "..."}`.
  - `ref` stays the front-end id, so existing clients and tests keep working;
    for built-ins, `ref` and `serve` are equal.
  - The `fe_pick` cookie holds the front-end id.
- **Admin preview** of any revision: the admin page mints a token for
  `rev/<id>` and embeds it in the same sandboxed iframe.
  - The iframe's `frame-ancestors` is `MAIN_ORIGIN`, and the admin is on it.
  - The admin page must load `frontend-host.js`-equivalent logic, or a
    preview variant, so the host protocol runs.

## Content contract

- `GET /api/site.json` returns the contract (JSON Schema in
  `docs/content-contract.json`):

  ```json
  { "contractVersion": 1,
    "pages":       [{ "slug": "", "title": "", "body": "", "_generated": [] }],
    "experiments": [{ "slug": "", "title": "", "summary": "", "_generated": [] }] }
  ```

- Every declared field is always present, with type defaults. Items may
  carry **extra fields**: observer-generated values, merged in as top-level
  keys and listed in `_generated`. Human values always win.
- Collections are `pages` and `experiments`, keyed by `slug`.

## Host API additions

| Member | |
|---|---|
| `site.get(path, fallback)` | dotted path into `site.content` (`"pages.0.title"`); never throws |
| `site.pages()`, `site.page(slug)`, `site.experiments()` | always an array or object (`{}` if not found) |
| `site.field(item, name, {expect, fallback})` | returns `item[name]` if it matches `expect`; otherwise returns `fallback` (default `""` / `[]`) **and reports** a gap or type break. `expect`: `"text"` (non-empty string), `"list"`, `"number"`, `"bool"`. Items need `_collection` and `slug`, which the host API adds during normalization. |

`site.field` details:

- `expect` defaults to `"text"`. Aliases: `string` → `text`, `array` → `list`,
  `boolean` → `bool`. An unknown value is treated as `text`.
- Default fallbacks: `""`, `[]`, `0`, `false`.
- `text` means a string with non-whitespace content.
- Lossless coercions return the coerced value and still report a type break:
  - a number or bool used as text;
  - a numeric string used as a number;
  - `"true"` or `"false"` used as a bool.
- Reports are sent only for items that carry `_collection`, so objects a
  front end builds itself are never reported.
- Reports are deduplicated per collection, item and field for each page load
  (`expect` isn't part of the key), capped at 50 per page load.
- Nothing in the host API throws.

## Messages additions

| Direction | type | fields |
|---|---|---|
| iframe → parent | `site:gap` | `collection`, `item`, `field`, `expect`, `got` (a type name: `missing`, `empty`, `string`, `number`, ...) |

`site:error` also reaches the observer (see below).

## Observer ingestion

- The parent forwards every `site:gap` and `site:error` (before and after
  ready) to `POST /api/observe`, fire-and-forget (`navigator.sendBeacon`),
  as JSON:

  ```json
  { "kind": "content-gap" | "type-break" | "frontend-error",
    "frontend": "<front-end id>", "serve": "<servable ref>", "route": "<slug>",
    "collection": "", "item": "", "field": "", "expect": "", "got": "",
    "message": "", "stack": "" }
  ```

  `kind` is `type-break` when `got` is a type name other than `missing` or
  `empty`.
- `POST /api/observe`:
  - same-origin, `Content-Type: application/json` or `text/plain` (beacons);
  - body at most 8 KB;
  - rate-limited per client IP, plus a global cap;
  - deduplicated by signature;
  - always replies `204` quickly;
  - it is public, so its input is untrusted data: never instructions.
- The parent also reports `frontend-error` when a front end times out or falls
  back. The message is prefixed with `"falling back to builtin/site: "` or
  `"falling back to the transcript: "`. For forwarded `site:error`s, the
  message is prefixed with the error kind (`"gpu-lost: "`, and so on).
- The client caps at 20 reports per page load, and clips fields (500 chars;
  message 1000; stack 3000).
- **Responses:**
  - every well-formed report gets `204`, including duplicates and
    rate-limited ones;
  - `403` for cross-origin requests (including `Origin: null`);
  - `413` for a body over 8 KB;
  - `415` for the wrong content type;
  - `400` for a malformed report.
- **Limits:**
  - per IP, a burst of 20 then one report every 6 s;
  - globally, a burst of 200 then 5/s;
  - both are in memory per instance.
- The client IP comes from `X-Forwarded-For`, counting `OBSERVE_XFF_HOPS` hops
  from the right: 1 on bare Cloud Run, **2 behind the load balancer**.
- Reports for unknown or unpublished items, or for front ends that aren't in
  the `frontends` table, are dropped.
- **Generated values are text only for now.** Gaps that expect `list`,
  `number` or `bool` go to review.

## Admin shell additions

- The admin nav has **Content**, **Builder** (`/admin/builder/`), **Observer**
  (`/admin/observer/`), and a `#observer-dot` badge.
- `/static/admin.js` fills the badge from
  `GET /admin/observer/unseen.json` → `{"unseen": N}`.

## Migrations

- `0002_builder.sql`: front-end revisions and builder sessions.
- `0003_observer.sql`: detections and generated fields.

Migrations are append-only; never renumber them.

---

# v1.2 additions (2026-10-01): the public builder

Any visitor can prompt a front end from the front page. Rules from
[frontends.md](frontends.md):

- no sign-up;
- drafts are visible only in the creating browser;
- submitting sends a front end to Ben's review;
- approved front ends join the rotation.

The admin builder stays as Ben's tool and uses the same engine.

## Identity

- **The `sid` cookie** holds a random 128-bit id. It's `HttpOnly`, `Secure` in
  prod, `SameSite=Lax`, `Path=/`, with a 30-day `Max-Age`, refreshed on use.
- The server stores only `sha256(sid)`.
- A front end created by a visitor has an **owner session**. Only that session
  can open its builder page, chat, preview, view it live, or submit it.
  Others get a 404, not a 403, so ids don't leak.

## Public routes (main site; POSTs behind CrossOriginProtection)

| Route | |
|---|---|
| `GET /build` | the public builder: prompt box and your front ends (this session) |
| `POST /build/new` | `prompt` (≤ 4000 chars), optional `title` → 303 `/build/<slug>#start=<prompt>` (name and slug derived as in the admin) |
| `GET /build/<slug>` | owner-only: chat, sandboxed preview, revision history, View live, Submit |
| `POST /build/<slug>/chat` | owner-only; same SSE stream format as the admin chat |
| `GET /build/preview?ref=rev/<id>` | owner-only → `{url}` |
| `POST /build/<slug>/live` | `on=1\|0`, owner-only. Sets or clears the `fe_live` cookie (`HttpOnly`, session cookie) naming the front-end id. |
| `POST /build/<slug>/submit` | `rev=<id>`, owner-only. Marks that revision submitted for review. |

Visitor-made front ends are `fe/<slug>` like Ben's. Their slugs share one
namespace.

## View live

`/api/frontend` checks `fe_live` first:

- If it names a front end owned by this session that has a revision, it serves
  that front end's latest revision. The response adds `"draft": true` and
  `"exit": "/build/<slug>/live"`.
- Otherwise the `fe_live` cookie is ignored and cleared.

`frontend-host.js` shows a small parent-page banner when `draft` is true:
"You're viewing your front end; only you can see this · Exit".

## Review

- Admin `/admin/builder/` gains a **Submissions** section, with a count on the
  nav dot. Each submission offers Preview, **Approve** and **Reject**.
  - **Approve** makes that revision active and adds the front end to the
    rotation.
  - **Reject** keeps the front end private.
- Visitors see their submission's status on `/build/<slug>`.

## Limits (env-configurable)

| Limit | Default |
|---|---|
| Concurrent runs per session | 1 |
| `BUILD_RUNS_PER_SESSION_HOUR` | 6 |
| `BUILD_RUNS_PER_IP_DAY` | 30 |
| `BUILD_RUNS_GLOBAL_DAY` | 25 (counted in the database, so it holds across instances) |
| `BUILD_MAX_PROMPT` | 4000 chars |

- When a limit is hit, the page shows a clear message, e.g. "The builder is
  resting for today; try again tomorrow." Never an error page.
- A model or API failure (including Anthropic's spend limit) shows "The builder
  is unavailable right now."
- Ben's admin builder is exempt from these limits.

## Entry point

The public shell draws a **"Make your own version of this site"** link:

- in the transcript header, so it works without WebGPU or JavaScript;
- as a small fixed button above the front-end iframe, drawn by the parent, so
  it appears over every front end.

Both go to `/build`.

## v1.2 as built (notes)

- **Limits:** `0` turns a limit off. Days are UTC.
- **Extra limit:** creating front ends is capped at 10 per IP per 10 minutes
  (in memory, per instance).
- **Messages:**
  - one run at a time: "One thing at a time…";
  - hourly cap: "Slow down a little…";
  - per-IP daily cap: "That's plenty of building for one day…";
  - site-wide daily cap: "The builder is resting for today…";
  - model or API failure: "The builder is unavailable right now…";
  - refusal: "Claude couldn't make that one…".
- **Reserved names:** `build` is a reserved route segment, and the slugs `new`
  and `preview` are reserved for visitors.
- **Admin routes:** `/admin/builder/pending.json` fills the `#builder-dot`
  badge on the Builder tab; `/admin/builder/submissions/{id}/approve|reject`
  handle review.
- **Submissions:** submitting the same revision again changes nothing. A newer
  revision replaces a pending submission. An approved revision stays active
  until a newer one is approved.
- **Observer:** visitor drafts don't report to it.
- **Dev only:** `BUILDER_DEMO_MODEL=1` (with `APP_ENV=dev`) swaps in an offline
  canned model; `e2e/run.sh` uses it.
- **Not yet built:**
  - purging drafts after 30 days;
  - a draft that falls back to the default front end still shows the "your
    front end" banner.

---

# v1.3 (2026-10-01): building on the site

The `/build` pages are replaced by a **modal on the live site**, and the live
site is the only preview. This supersedes the v1.2 route table.

## Entry and modal

- The corner button, the transcript link and `/?build=1` all open the modal in
  place. The modal reads `?build=1`, then strips it from the URL.
- `/build`, `/build/` and `/build/<slug>` redirect (302) to `/?build=1`.
- Inside the modal:
  - **Build** (prompt-first);
  - streaming progress;
  - **Your creations**: every front end with all its versions. Picking a
    version switches the site to it in place.
  - **Change it**: reprompt from the version currently on the site;
  - **Submit version N for review**, with its status;
  - **Back to the normal site**.
- While a build runs, closing the modal minimizes it to a pill.
- The modal is accessible: `role=dialog` with focus trap, Esc to close, and
  focus returns to whatever opened it.

## API (owner-only; 404 for non-owners; POSTs behind CrossOriginProtection; limit refusals come back as `{"error": "<friendly message>"}`)

| Route | |
|---|---|
| `GET /build/api/frontends` | `canBuild`, any limit notice, `maxPrompt`, the current live selection, and each front end with status, running flag and versions |
| `POST /build/api/new` | `{prompt, title?}` → 201 `{id, slug, title}` |
| `POST /build/api/fe/{slug}/chat` | SSE, same format as the admin chat |
| `POST /build/api/fe/{slug}/live` | `{rev}` (empty means the latest version) → sets `fe_live` |
| `POST /build/api/fe/{slug}/submit` | `{rev}` → `{status, message}` |
| `POST /build/api/exit` | clears `fe_live` → 204 |

Reserved visitor slugs: `new`, `preview`, `api`.

## View live, version-specific

- **`fe_live`** is `"<front-end id>:<revision id>"`. It's a session cookie,
  `HttpOnly` and `SameSite=Lax`.
- `/api/frontend` serves exactly that version, but only if this session owns
  the front end and the version belongs to it. Anything else is cleared. A
  legacy value without a version means the latest.
- Draft responses add `revision`, `number`, `title`, and
  `exit: "/build/api/exit"`.

## Parent page additions

`frontend-host.js` exposes `window.siteHost`:

- `reload()` reloads the iframe with a fresh `/api/frontend`, without reloading
  the page;
- `exitDraft()`;
- `current()`.

It fires a `sitehost:load` event on each load. The modal uses these to switch
the site in place. `builder-stream.js` is the SSE chat client shared by the
modal and the admin builder.

## v1.3 addition: house fonts

- The user-content service serves the house fonts at `/fonts/<file>`.
  - Fonts: Newsreader, Instrument Sans and Fragment Mono, all SIL OFL 1.1, as
    WOFF2.
  - Embedded in the binary; `font/woff2`, CORS `*`, one-year immutable cache.
- Front ends, including visitor-generated ones, may use them with `@font-face`.
  `font-src 'self'` covers it because it's the same origin.
- The main site serves the same files at `/static/fonts/`.
- The builder system prompt lists the paths and gives an `@font-face` snippet.
