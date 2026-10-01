# Vibe-coded front ends

Design agreed 2026-09-29. Nothing here is built yet.

Anyone can open a builder on the site, chat with an LLM, and vibe code a complete
front end for the site: HTML, JS, WebGPU, WGSL. They can view their creation on
the live site in their own browser session. If they submit it, Ben reviews it,
and approved front ends join a random rotation shown to all visitors.

## Principles

1. **The parent page is always Ben's.** Every URL is served by the Go server
   with the CMS transcript (semantic HTML for search engines, screen readers and
   the no-WebGPU fallback), the URL/history, and the choice of front end. A front
   end only ever draws inside a sandboxed iframe.
2. **Untrusted code never runs with Ben's authority.** Front ends run on a
   separate user-content domain, in a sandboxed iframe with a strict CSP and no
   network access beyond what the host hands them.
3. **No creation is reachable by link until Ben approves it.** A draft is
   visible only in the browser session that made it. Nothing unapproved is ever
   addressable by a URL, so nothing offensive can circulate with Ben's name on it.
4. **Front ends always try to show the CMS content.** The host API delivers it,
   and the builder agent adds it when a user's request leaves it out.
   Suppressing it takes deliberate effort, and review checks for it.

## Architecture

```
benpriddy.com (Go, trusted)                      user-content domain (untrusted)
┌──────────────────────────────────────────┐    ┌─────────────────────────────────┐
│ transcript (CMS → HTML)                  │    │ separate Cloud Run service       │
│ picks front end: approved rotation, or   │    │ serves front-end files from GCS  │
│   the session's own draft                │    │ at /t/<signed token>/<file>      │
│ mints a signed URL, embeds it in a       │───▶│ verifies token on every request  │
│   sandboxed <iframe>                     │    │ strict CSP, frame-ancestors =    │
│ owns URL/history, /admin, Claude API key │◀──▶│   Ben's domain only              │
└──────────────────────────────────────────┘ postMessage (host API)─────────────────┘
```

This is the established pattern for running untrusted web code: a separate
user-content domain plus sandboxed iframes (CodePen `cdpn.io`, JSFiddle,
`githubusercontent.com`, `googleusercontent.com`, `claudeusercontent.com`),
with access gated by expiring signed URLs, the same mechanism as Cloud Storage
and S3 signed URLs. Every piece is standard browser and HTTP behavior; nothing
depends on a custom loader.

### Serving: separate domain, separate service

- Front ends are served from a **separate registrable domain** (not a subdomain
  of Ben's), so browsers treat it as a different site: no shared cookies or
  storage, and no same-site privileges.
- The user-content server is a **separate Cloud Run service**. It has read-only
  access to the front-end bucket and one secret (the URL-signing key). It has
  no database credentials, no admin, and no Claude API key, so even a bug in it
  exposes nothing else.
- Files are served as ordinary static files, so relative paths, ES modules,
  wasm and workers behave exactly as in development. Responses carry
  `Access-Control-Allow-Origin: *` (the sandboxed document has an opaque
  origin, and module scripts and wasm are fetched with CORS; no credentials are
  ever involved).

### Access: signed, expiring URLs (how "no links" is enforced)

1. The parent page, on Ben's domain, authenticates the visitor by their session
   cookie and decides what they may see: an approved front end, or their own
   draft. It never mints a URL for someone else's draft.
2. It mints a URL for that front end,
   `https://<usercontent>/t/<token>/index.html`. The token is an HMAC-signed
   `{front end or draft revision id, issued-at}`. It lives in the path so every
   relative subresource URL carries it automatically.
3. The user-content server verifies the signature on every request and serves
   only files of that front end:
   - `index.html` only within **60 seconds** of issue;
   - other files within 30 minutes (for lazy-loaded assets).
4. The page can only load inside Ben's iframe:
   - `Content-Security-Policy: frame-ancestors <Ben's domain>` blocks
     embedding on any other site;
   - `index.html` is refused unless the browser's Fetch Metadata header says
     `Sec-Fetch-Dest: iframe`, so opening the URL in a tab gets nothing.
5. `Referrer-Policy: no-referrer` keeps tokens out of referrers.

A copied URL is dead within a minute, can't be opened directly or embedded
elsewhere, and is only ever minted for the owning session. Screenshots can't be prevented, but no
unapproved creation is reachable by link.

### Sandbox

- `<iframe sandbox="allow-scripts">`: no `allow-same-origin`, `allow-forms`,
  `allow-top-navigation`, `allow-popups` or `allow-modals`. Each front end runs
  in a unique opaque origin: no cookies or storage, and it can't read any other
  front end. It also can't submit forms (no fake logins), redirect the page, or
  open popups.
- Response CSP from the user-content server:
  - `sandbox allow-scripts`, the same restrictions as the iframe attribute but
    enforced by the server, so they apply however the page is reached;
  - `default-src 'self'`, and `script-src 'self' 'wasm-unsafe-eval'`, so code
    runs only from the front end's own files;
  - `connect-src 'self'`, so no calls to outside servers and no data
    exfiltration;
  - `frame-ancestors` restricted to Ben's domain.
- The parent watches for failure: if there's no `ready` message within a
  timeout, or an uncaught error or lost GPU device is reported, it swaps in the
  default front end.
- Built-in front ends (Rust `site/`, particle-stream) are served the same way,
  so they must use relative asset paths (trunk `--public-url ./`).

### Host API (inside the iframe, over postMessage)

Front ends load the host API with `<script src="/site-host.js">`, a fixed
script served by the user-content service itself (not part of the project), so
every front end speaks the same protocol. The parent only accepts messages whose
`event.source` is the iframe it created.

| API | Purpose |
|---|---|
| `site.content` | published pages and experiments, the same data as `/api/site.json` |
| `site.route` / `site.onRoute(fn)` | current page slug; notified when the parent's URL changes |
| `site.navigate(slug)` | ask the parent to navigate (the parent owns history) |
| `site.textCanvas(text, opts)` | helper: text → canvas, so rendering content is easy in WebGPU |
| `site.ready()` / `site.reportError(e)` | lifecycle and error reporting (feeds the builder's repair loop) |

Ben's own front ends, the Rust `site/` crate and particle-stream, move onto the
same host API as **built-in front ends**. `site/` is the default and the fallback.

## The builder

1. **Identity:** anonymous. A session cookie is issued on first visit; no
   sign-up.
2. **Chat:** the Go server runs the conversation against the Claude API
   (`claude-opus-5-5`, adaptive thinking, streamed to the browser over SSE). The
   API key never reaches the browser. The model edits a multi-file project
   through file tools (write file, replace text in a file).
3. **Preview:** the live site itself is the preview (visitors), or the sandboxed
   iframe with a freshly signed URL.
   Console errors, exceptions and WebGPU validation errors are sent back to the
   model automatically so it can repair them.
4. **View live:** the session sees the whole site with its draft as the front
   end, in that browser session only. It is never shown to anyone else.
5. **Submit:** freezes an immutable snapshot (files, chat transcript, thumbnail
   from the preview canvas) into Ben's review queue in `/admin`.
6. **Review:** Ben previews the snapshot in the same sandbox, reads the source
   and chat, then approves or rejects. Approved snapshots join the rotation.

Generated front ends are JS + WebGPU + WGSL, because vibe coding needs a
sub-second preview loop. Generated Rust (via a Cloud Build compile service)
could come later.

### Builder agent instructions: content

The builder's system prompt must include, in substance:

> This front end is for Ben Priddy's personal site. Its job is to present the
> site's content: the pages and experiments available from `site.content`, for
> the page named by `site.route`. Always render that content legibly and make
> navigation between pages possible via `site.navigate`, whatever visual style
> the user asks for. If the user's request doesn't say how the content should
> appear, choose a way that fits their design and include it anyway. Only omit
> or obscure the content if the user explicitly insists, and in that case tell
> them the front end will likely not be approved for the public rotation.

Review then checks that approved front ends actually show the content.

## Rotation

- Each visit picks one approved front end at random and keeps it for the whole
  visit, so navigating doesn't switch styles mid-browse.
- A visitor's own draft (in the session that made it) overrides the rotation
  only in that session, via "view live".

## Data

Stored in the database, with project files and thumbnails in Cloud Storage:

- **sessions:** anonymous identities
- **projects:** belong to a session
- **drafts:** current files per project
- **chats:** the conversation behind each project
- **snapshots:** immutable, created on submit
- **reviews:** status and notes
- **rotation:** approved snapshots

Unsubmitted drafts are purged 30 days after their last activity.

## Deferred

- Concurrency limits and per-session / global spending caps (all LLM traffic
  goes through the Go session endpoint, which is where they'll go).
- Optional sign-in so creators can keep projects across devices.
- Generated Rust front ends.

## Decisions (2026-09-29)

- **Rotation:** chosen per visit, not per page view.
- **Default and fallback front end:** the Rust `site/` crate.
- **Identity:** anonymous. "Only in that browser session" means that browser:
  a 30-day sliding cookie, so closing a tab doesn't lose work. Clearing cookies
  loses unsubmitted drafts.
- **Draft retention:** 30 days after last activity; snapshots and approved
  front ends are kept.
- **Database:** Cloud SQL for PostgreSQL (smallest instance), `pgx`, migrations
  in the repo. Local dev keeps the in-memory store until the Postgres store
  lands.
- **Files:** a Cloud Storage bucket for project files, snapshots and thumbnails.
- **User-content domain:** `benpriddy-usercontent.com` (registered at GoDaddy
  2026-09-30), served by its own Cloud Run service. Never a
  subdomain of the main site.
- **Running front ends:** real files on the user-content domain, sandboxed
  iframe, strict CSP, HMAC-signed expiring URLs (see Architecture).
- **Region:** `us-central1` for Cloud Run, Cloud SQL and the bucket (moved from
  `us-east1`, where Cloud Run wouldn't start revisions; see deploy.md).
- **GCP project:** `benpriddycom` (dedicated to the site, billing linked).
- **Main domain:** `benpriddy.com` (DNS currently at GoDaddy).
- **Admin auth:** Google sign-in inside the app, restricted to an allowlist of
  email addresses supplied via the `ADMIN_EMAILS` env var (not committed).
  Replaces basic auth.
- **LLM:** Claude API, `claude-opus-5-5`, key in Secret Manager. A hard monthly
  spend limit is set in the Anthropic Console as a safety net until the in-app
  caps land.
