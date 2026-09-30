# personal-site-2026

Monorepo for Ben Priddy's personal site: a WebGPU/wasm site, a Go server with an
admin CMS, and standalone interactive experiments.

## How the site works

- **Parent page (Go, trusted).** Every URL is served by the main server with
  the CMS content as a semantic HTML *transcript* (for search engines, screen
  readers, and browsers without WebGPU). It picks a front end at random per
  visit and shows it in a sandboxed iframe.
- **Front ends (untrusted by design).** Served only by a separate
  **user-content service** on its own domain, behind short-lived signed URLs,
  with a sandbox CSP. They get content and navigation over `postMessage`
  (`/site-host.js`). Built-ins: `builtin/site` (the Rust `site/` app, default
  and fallback) and `builtin/particle-stream`.
- **Fallback.** If a front end fails, the default loads; if that fails too, the
  transcript shows as a plain HTML site.
- **Admin.** `/admin/` is a plain `html/template` CMS.
- **Planned: vibe-coded front ends.** Anyone can build a front end by chatting
  with an LLM; approved ones join the rotation.

Design: [`docs/frontends.md`](docs/frontends.md). Contract:
[`docs/frontend-protocol.md`](docs/frontend-protocol.md). Plan:
[`docs/restructure-plan.md`](docs/restructure-plan.md).

**Status: runs locally, not deployed.** PostgreSQL store with migrations (or
in-memory when `DATABASE_URL` is unset in dev); GCP hosting comes next.

## Layout

```
cmd/server/            main site (config, graceful shutdown for Cloud Run)
cmd/usercontent/       user-content service (serves front ends)
cmd/devdb/             local PostgreSQL 17 for dev, no root (embedded-postgres)
internal/
  config/              env-based settings (shared)
  content/             CMS content types (Page, Experiment)
  store/               Store interface; Postgres (pgx) + migrations/, in-memory stand-in
  auth/                admin guard (basic auth, stopgap)
  server/              public pages, /api/frontend, /api/site.json, /admin
  usercontent/         signed-URL file serving, sandbox headers
  fetoken/             HMAC capability tokens for front-end URLs
  frontend/            front-end refs and the rotation
web/
  templates/public/    page shell (transcript) and transcript pages
  templates/admin/     admin CMS layout and forms
  static/              CSS + frontend-host.js (parent side of the protocol)
  usercontent/         site-host.js (the in-iframe host API)
site/                  builtin/site: Rust → wasm → wgpu, built by trunk
experiments/
  particle-stream/     builtin/particle-stream
e2e/                   Playwright end-to-end tests (headless Chromium + WebGPU)
infra/                 Terraform for GCP (not written yet)
docs/                  design, protocol, plan
scripts/               dev-setup.sh (toolchain, no root), build-frontends.sh
Dockerfile             two images: --target server / --target usercontent
Makefile               dev / build / test / frontends / e2e / docker
```

## Run locally

Needs Go 1.27+, Rust with the `wasm32-unknown-unknown` target, trunk 0.21, a C
toolchain, and Node for the e2e tests. `scripts/dev-setup.sh` installs the
non-Node parts into `$HOME` without root (Zig as the C compiler):

```sh
scripts/dev-setup.sh
export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$HOME/.local/go/bin:$PATH"
```

```sh
go run ./cmd/devdb                  # Postgres 17 on :5433, data in build/pgdata (Ctrl-C to stop)
export DATABASE_URL='postgres://site:site@localhost:5433/site?sslmode=disable'
scripts/build-frontends.sh          # → build/frontends/builtin/{site,particle-stream}/
PORT=8091 go run ./cmd/usercontent  # user-content service, http://127.0.0.1:8091
PORT=8090 go run ./cmd/server       # main site, http://localhost:8090 (admin: admin / dev)
```

The main site applies migrations on startup. Without `DATABASE_URL` it uses
the in-memory store (dev only; edits vanish on restart).

(`make frontends` and `make dev` do the same if `make` is installed.) Dev uses
8090/8091 because 8080/8081 are taken by other local services. `localhost`
and `127.0.0.1` are different sites to the browser, which is what makes the
local sandbox realistic.

```sh
go test ./...                       # unit tests + store tests on a real Postgres (-short skips it)
(cd e2e && npm ci) && e2e/run.sh    # end-to-end: builds everything, runs 3 phases
e2e/run.sh --slow                   # + the 61s token-expiry test
```

Environment (both binaries): `PORT`, `APP_ENV` (`dev`|`prod`),
`FRONTEND_SIGNING_KEY` (required in prod), `MAIN_ORIGIN`
(`http://localhost:8090`), `USERCONTENT_ORIGIN` (`http://127.0.0.1:8091`),
`FRONTENDS_DIR` (`build/frontends`). Main site only: `DATABASE_URL` (required in prod), `ADMIN_USER` (`admin`),
`ADMIN_PASSWORD` (`dev` in dev, required in prod), `FRONTEND_ROTATION` (comma-
separated refs; overrides the rotation, used by tests).

## Routes

Main site:

| Route | What |
|---|---|
| `/` | home page (transcript) + the front-end iframe |
| `/<slug>` | CMS page |
| `/experiments/` | experiment listing |
| `/api/frontend` | this visit's front end: `{ref, url}` with a fresh signed URL |
| `/api/site.json` | published CMS content (sent to front ends) |
| `/admin/` | CMS: pages, experiment listings, front-end rotation |
| `/health` | health check |

User-content service: `/t/<token>/...` (front-end files), `/site-host.js`,
`/health`.

## Hosting

- **Now:** none. Vercel was retired on 2026-09-29.
- **Planned:** Cloud Run in GCP project `benpriddycom`: the main site at
  `benpriddy.com` and the user-content service at `benpriddy-usercontent.com`
  (both domains verified).
