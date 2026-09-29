# personal-site-2026

Monorepo for Ben Priddy's personal site: a WebGPU/wasm site, a Go server with an
admin CMS, and standalone interactive experiments.

## How the site renders

- **Canvas** — the public site is a Rust → wasm → wgpu app (`site/`) that draws
  everything on one full-screen canvas, reading content from `/api/site.json`.
- **Transcript** — the Go server renders the same CMS content as semantic HTML
  into every page (`web/templates/public/`). It is visually hidden when WebGPU
  runs, but it's what search engines and screen readers read (the canvas is
  `aria-hidden`).
- **Fallback** — without WebGPU, or if the wasm app fails, the transcript is
  shown as a plain HTML site.
- **Admin** — `/admin/` is a plain `html/template` CMS.
- **Planned: vibe-coded front ends.** Anyone can build a front end by chatting
  with an LLM; approved ones rotate for all visitors. See
  [`docs/frontends.md`](docs/frontends.md).

**Status: scaffolded, not deployed.** The Go server runs locally with an
in-memory store; the database and GCP hosting are next. See
[`docs/restructure-plan.md`](docs/restructure-plan.md).

## Layout

```
cmd/server/          entrypoint (config, graceful shutdown for Cloud Run)
internal/
  config/            env-based settings
  content/           CMS content types (Page, Experiment)
  store/             Store interface + in-memory placeholder
  auth/              admin guard (basic auth, stopgap)
  server/            routes: public site, /admin, /experiments/<slug>/
web/
  templates/public/  page shell (canvas + transcript) and transcript pages
  templates/admin/   admin CMS layout and forms
  static/            CSS, embedded into the binary
site/                the public site: Rust → wasm → wgpu, built by trunk
experiments/
  particle-stream/   Rust → wasm → WebGPU particle field
infra/               Terraform for GCP (not written yet)
docs/                plans and architecture notes
scripts/             dev-setup.sh: user-space toolchain install (no root)
Dockerfile           Cloud Run image: builds site + experiments + server
Makefile             run / build / vet / site / experiments / docker
```

## Run locally

Needs Go 1.27+. The wasm builds also need Rust, the `wasm32-unknown-unknown`
target, trunk 0.21, and a C toolchain for build scripts.
`scripts/dev-setup.sh` installs all of it into `$HOME` without root (using Zig
as the C compiler), then add it to PATH:

```sh
scripts/dev-setup.sh
export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$HOME/.local/go/bin:$PATH"
```

```sh
make site           # build the wasm site into site/dist
make experiments    # build experiments so /experiments/<slug>/ serves them
make run            # http://localhost:8080, admin at /admin/ (admin / dev)
```

Without `make site`, the server serves the transcript only.

Environment: `PORT` (8080), `APP_ENV` (`dev`|`prod`), `SITE_DIR`
(`site/dist`), `EXPERIMENTS_DIR` (`experiments`), `ADMIN_USER` (`admin`), `ADMIN_PASSWORD` (`dev` in dev,
required in prod).

## Routes

| Route | What |
|---|---|
| `/` | home page (CMS page with empty slug) + published experiments |
| `/<slug>` | CMS page |
| `/api/site.json` | published CMS content for the wasm site |
| `/site/` | the built wasm site bundle |
| `/experiments/` | experiment index |
| `/experiments/<slug>/` | the experiment's built `dist/` |
| `/admin/` | CMS: edit pages and experiment listings |
| `/healthz` | health check |

## Experiments

Each experiment is a self-contained workspace under `experiments/<slug>/` with
its own build and README, built with `--public-url /experiments/<slug>/`. The
CMS stores only its listing (title, summary, published, order); the server
serves files only for slugs the CMS knows about. Build output (`target/`,
`dist/`) is gitignored.

| Experiment | Stack | README |
|---|---|---|
| particle-stream | Rust, wgpu, WebGPU, trunk | [experiments/particle-stream/README.md](experiments/particle-stream/README.md) |

## Hosting

- **Now:** none. Vercel was retired on 2026-09-29; nothing deploys.
- **Planned:** Cloud Run, running the root `Dockerfile` image. See `infra/`.
