# personal-site-2026

Monorepo for Ben Priddy's personal site: a container site with a CMS that hosts
standalone interactive experiments.

**Status: mid-restructure.** Only step 1 is done: the original site (a WebGPU
particle piece) moved into `experiments/particle-stream/`. The container site
and CMS are not built yet. See [`docs/restructure-plan.md`](docs/restructure-plan.md).

## Layout

```
experiments/
  particle-stream/   Rust → wasm → WebGPU particle field (live site today)
docs/                plans and architecture notes
vercel.json          current hosting (serves particle-stream at the domain root)
vercel-build.sh      Vercel build script (installs trunk, builds the experiment)
```

Planned, not yet present: a Go CMS / container site at the repo root, and
Terraform for GCP hosting (both already have `.gitignore` entries).

## Experiments

Each experiment is a self-contained workspace under `experiments/<name>/` with
its own build and README. Build output (`target/`, `dist/`) is gitignored.

| Experiment | Stack | README |
|---|---|---|
| particle-stream | Rust, wgpu, WebGPU, trunk | [experiments/particle-stream/README.md](experiments/particle-stream/README.md) |

## Hosting

- **Now:** Vercel builds `experiments/particle-stream` and serves its `dist/`
  at the domain root.
- **Planned:** GCP, with the Go container site at the root and experiments
  embedded under `/experiments/`.
