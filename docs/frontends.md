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
benpriddy.com (Go, trusted)                         user-content domain (untrusted)
┌─────────────────────────────────────────┐        ┌────────────────────────────────┐
│ transcript (CMS → HTML)                 │        │ runner page (static, generic)  │
│ picks front end: approved rotation, or  │ iframe │  receives files via postMessage│
│   the session's own draft               │◀─────▶│  boots them from blob: URLs    │
│ owns URL/history, /admin, Claude API key│postMsg │  exposes the host API (site.*) │
└─────────────────────────────────────────┘        └────────────────────────────────┘
```

### The runner: how "no links" is enforced

The user-content domain serves one static **runner** page and nothing else. It
has no per-project URLs. The runner bundles the received files into the page
(`data:` URLs through an import map, plus a `fetch` shim for relative asset
paths), which avoids cross-origin `blob:` restrictions inside the sandbox. The
first prototype has to prove this works, including for a trunk-built wasm
front end.

1. The parent page, on Ben's domain and authenticated by the visitor's session
   cookie, fetches the front end's files from the Go API:
   - an approved front end from the rotation, or
   - the session's own draft, which only that session is allowed to fetch.
2. The parent posts the files into the runner iframe.
3. The runner boots them from `blob:` URLs.

A draft therefore has no address anyone could copy. It exists only as bytes
handed from an authenticated parent page to a sandbox. Approved front ends load
the same way, so there is one code path. Screenshots can't be prevented, but
nothing unapproved lives at a URL on Ben's domains.

### Sandbox

- `<iframe sandbox="allow-scripts">`: no `allow-same-origin`, `allow-forms`,
  `allow-top-navigation`, `allow-popups` or `allow-modals`. That blocks cookie
  access, fake login forms, redirects and popups.
- Runner CSP: `default-src 'none'`; scripts, styles, images, workers and wasm
  only from `data:`/`blob:` and the runner's own origin; no `connect-src` to
  the outside world.
- The user-content domain is a separate registrable domain, so it shares no
  cookies with Ben's domain.
- The parent watches for failure: if there's no `ready` message within a
  timeout, or an uncaught error or lost GPU device is reported, it swaps in the
  default front end.

### Host API (inside the iframe, over postMessage)

| API | Purpose |
|---|---|
| `site.content` | published pages and experiments, the same data as `/api/site.json` |
| `site.route` / `site.onRoute(fn)` | current page slug; notified when the parent's URL changes |
| `site.navigate(slug)` | ask the parent to navigate (the parent owns history) |
| `site.textTexture(text, opts)` | helper: text → canvas/texture, so rendering content is easy in WebGPU |
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
3. **Preview:** after each edit the builder reloads the draft in the runner.
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
- **User-content domain:** a separate registrable domain, e.g.
  `<main-domain>-usercontent.com`. Never a subdomain of the main site.
- **Region:** `us-east1` for Cloud Run, Cloud SQL and the bucket.
- **GCP project:** a new, dedicated project for the site.
- **Admin auth:** Google sign-in inside the app, restricted to an allowlist of
  email addresses supplied via the `ADMIN_EMAILS` env var (not committed).
  Replaces basic auth.
- **LLM:** Claude API, `claude-opus-5-5`, key in Secret Manager. A hard monthly
  spend limit is set in the Anthropic Console as a safety net until the in-app
  caps land.
