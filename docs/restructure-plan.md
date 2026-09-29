# Restructure plan: container site + CMS

The original plan ("polymorphic-skipping-pascal") was a Claude plan file on
another machine and wasn't committed. It was reconstructed on 2026-09-29 from
commit messages (`94a81fe`, `6e99664`) and `.gitignore`, then updated with the
decisions below.

## Goal

Turn the repo from a single particle-art site into a container site with a CMS,
where the particle piece becomes one of several embedded experiments.

## Decisions (2026-09-29)

- **Server** — Go at the repo root, standard library only.
- **Public site** — a full-canvas Rust → wasm → wgpu app (`site/`). The server
  transpiles CMS content into a semantic HTML *transcript* in every page for
  search engines and screen readers; without WebGPU the transcript is shown
  as a plain HTML site.
- **CMS** — an `html/template` admin UI (`/admin/`) backed by a database.
- **Home page** — a designed index page (bio, work) linking to experiments.
- **Hosting** — Cloud Run, one container image built by the root `Dockerfile`.
- **Experiments** — sandboxed workspaces under `experiments/<slug>/`, built
  into the image and served at `/experiments/<slug>/` (Rust ones use trunk
  `--public-url`). The CMS stores their listing metadata only.
- **Vercel** — retired 2026-09-29. Nothing is live until GCP is up.

## Steps

- [x] **Pare back the particle piece** (`94a81fe`, 2026-07-02 onward).
- [x] **Monorepo move** (`6e99664`, 2026-07-05).
- [x] **Cleanup** (2026-09-29): Pages workflow removed, docs refreshed.
- [x] **Retire Vercel** (2026-09-29).
- [x] **Scaffold the Go site + CMS** (2026-09-29): routes, templates, admin
  forms, `Store` interface with an in-memory placeholder, Dockerfile.
- [x] **Wasm site scaffold** (2026-09-29): `site/` crate (placeholder wgpu
  scene reading `/api/site.json`), public shell with transcript + fallback.
- [ ] **Build the canvas site**: text rendering, layout, navigation (History
  API, so URLs match the transcript pages), experiment transitions.
- [ ] **Choose and wire the database** behind `internal/store.Store`, with
  migrations.
- [ ] **Real admin auth** — replace basic auth with session login or IAP.
- [ ] **Terraform** (`infra/`): Artifact Registry, Cloud Run, Secret Manager,
  database, domain.
- [ ] **CI/CD** to build and deploy the image on push to `main`.
- [ ] **Design** the home page and site styling.
- [ ] **Point DNS** at Cloud Run.

## Open questions

- **Database.** Cloud Run instances have no persistent disk, so the options are:
  - Cloud SQL Postgres: familiar SQL, but costs roughly $10/month even when idle.
  - Firestore: free tier, no server to run, but a document model.
  - SQLite replicated to GCS with Litestream: cheap and simple, but limited to one instance.
- **Admin auth**: username + password sessions, or Google login via IAP?
- Does the CMS eventually replace particle-stream's build-time
  `phrases.json` / `dials.json`?
- Page body format: markdown, or a richer block editor? The transcript and the
  canvas both need to render it.
- Canvas text: rasterize with 2D canvas (as particle-stream does) or SDF/MSDF
  font atlases?
- Should particle-stream share code with the site (a Cargo workspace / shared
  crate)?
