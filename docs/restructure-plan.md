# Restructure plan: container site + CMS

Reconstructed 2026-09-29. The original plan ("polymorphic-skipping-pascal") was
a Claude plan file on another machine and wasn't committed; this is rebuilt from
commit messages (`94a81fe`, `6e99664`) and `.gitignore`. **Fill in the gaps
marked TODO.**

## Goal

Turn the repo from a single particle-art site into a container site with a CMS,
where the particle piece becomes one of several embedded experiments.

## Target architecture

- **Container site + CMS** — Go, at the repo root (`.gitignore` already
  expects `air` hot-reload output in `/tmp/` and binaries in `/server`, `/bin/`).
- **Experiments** — sandboxed workspaces under `experiments/<name>/`, each built
  independently and embedded by the container under `/experiments/<name>/`
  (built with `trunk --public-url` for the Rust ones).
- **Hosting** — GCP, provisioned with Terraform (`.gitignore` already covers
  `.terraform/`, state and `*.tfvars`).
- **Transition** — Vercel served particle-stream at the domain root until
  2026-09-29, when it was retired ahead of GCP. There's no live deploy in the
  meantime.

## Steps

- [x] **Pare back the particle piece** (`94a81fe`, 2026-07-02 onward): remove
  drag/nav, park it under `parked/drag-nav/`, drop the name, center the
  phrase, revert `sections.json` → `phrases.json`.
- [x] **Monorepo move** (`6e99664`, 2026-07-05): `git mv` the site into
  `experiments/particle-stream/`, repoint the Vercel build, keep the live
  site unchanged.
- [x] **Cleanup** (2026-09-29): remove the broken GitHub Pages workflow, docs
  refresh, park the orphaned `interaction_intent.rs`.
- [ ] **Scaffold the Go CMS** at the repo root.
- [ ] **Embed particle-stream** under `/experiments/particle-stream/` with
  `--public-url`.
- [ ] **Terraform + GCP** hosting; CI/CD to build the Go server and experiments.
- [x] **Retire Vercel** (2026-09-29): removed `vercel.json` /
  `vercel-build.sh`.
- [ ] **Point DNS** at GCP.

## Open questions (TODO)

- What the CMS manages: pages, experiment metadata, the phrases, the bio?
- Does the CMS replace the build-time `phrases.json` / `dials.json` baking?
- Which GCP service (Cloud Run, GCE, GCS + load balancer)?
- What is on the root page: the particle piece as the hero, or an index of
  experiments?
- Go framework / templating choice; database or file-based content.
