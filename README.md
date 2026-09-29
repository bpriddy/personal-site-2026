# personal-site-2026

Monorepo for Ben Priddy's personal site: a Go container site with an admin CMS
that hosts standalone interactive experiments.

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
  templates/         html/template pages (base + one file per page), admin/
  static/            CSS, embedded into the binary
experiments/
  particle-stream/   Rust → wasm → WebGPU particle field
infra/               Terraform for GCP (not written yet)
docs/                plans and architecture notes
Dockerfile           Cloud Run image: builds experiments + server
Makefile             run / build / vet / experiments / docker
```

## Run locally

Needs Go 1.27+.

```sh
make run            # http://localhost:8080, admin at /admin/ (admin / dev)
make experiments    # build experiments so /experiments/<slug>/ serves them
                    # (needs Rust + wasm32-unknown-unknown + trunk)
```

Environment: `PORT` (8080), `APP_ENV` (`dev`|`prod`), `EXPERIMENTS_DIR`
(`experiments`), `ADMIN_USER` (`admin`), `ADMIN_PASSWORD` (`dev` in dev,
required in prod).

## Routes

| Route | What |
|---|---|
| `/` | home page (CMS page with empty slug) + published experiments |
| `/<slug>` | CMS page |
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
