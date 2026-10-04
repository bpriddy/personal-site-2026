# Builder connections and skills

The front-end builder (`internal/builder`) can reach outside services during a
run through **connections** (`internal/connect`), and reads **skills**
(`internal/builder/skills/*.md`) on demand. Both exist so a prompt like "a Star
Wars crawl with X-wings" can use a real X-wing model, and so the model only
carries technique guides when it needs them.

## How it fits together

- A connection implements `connect.Connection`: a name, its tools (name,
  description, input properties; all required, strict tool use) and `Call`.
  `Call` returns text for the model, optional image URLs (thumbnails the model
  sees as image blocks in the tool result), and, for an import whose license
  requires attribution, a `Credit`.
- The builder declares every configured connection's tools after the file
  tools and `read_skill`, and lists the connections and skills in a third
  system block (`ToolsBrief`), cached with the rest of the static prompt.
- Imports are copied into the media store under `/media/assets/...` (served
  by both services; `internal/media.ServableName`). Front ends run in a
  sandbox whose CSP allows only the user-content origin, so nothing is
  hot-linked. Asset names are immutable and shared: a second import of the
  same model reuses the stored file.
- **Credits:** `finish` refuses to save while the files use an imported asset
  (its `/media/` path appears in them) but don't contain the credit's required
  text (the author). The model is told the exact credit line.
- **Per-run budgets:** `connect.Spend` caps imports per run (4 models, 4
  textures, 3 font families). Visitors' runs are also bounded by the public
  builder's limits.

## The connections

| Name | Tools | Needs | License handling |
|---|---|---|---|
| `sketchfab` | `sketchfab_search`, `sketchfab_import` | `SKETCHFAB_API_TOKEN` (Sketchfab → Settings → Password & API) | only CC0, CC BY, CC BY-SA, downloadable, ≤ 15 MB, ≤ 300k faces; the .glb must be self-contained and not Draco/meshopt/Basis compressed (`connect.InspectGLB`) |
| `polyhaven` | `polyhaven_search_textures`, `polyhaven_import_texture` | nothing | CC0 |
| `googlefonts` | `google_fonts_import` | nothing | OFL / Apache; latin and latin-ext woff2 only |
| `krea` | `krea_generate_image` | `KREA_API_KEY` (secret `krea-api-key`) | the generated image is the site's; one model only (Krea 2 medium, 1K, about $0.03), 4 per run, copied to `/media/assets/images/krea/` and shown back to the model |
| `kreavideo` | `krea_generate_video` | `KREA_API_KEY`; Ben's runs only (`connect.BenOnly`: not visitors', not the observer's) | Seedance 2.5 (720p) or MiniMax H3, 4-12 s; 2 clips a run; an estimated-spend cap per UTC day (`KREA_VIDEO_DAILY_USD`, default 15; per instance, in memory), refunded when a job fails; MP4 copied to `/media/assets/videos/krea/` |

`BUILDER_CONNECTIONS` (comma list) limits which are on; by default all are,
Sketchfab and Krea only when their keys are set.

**MCP vs a connection.** Krea also runs a hosted MCP server (all its image and
video models). The builder uses a plain connection instead, because builder
runs (visitors' included) need a fixed, cheap model and a per-run cap, and the
result must be copied into the media store before a front end can load it.
The MCP server is for interactive work (Claude Code, Claude), not runs. Without a writable media store
there are none.

## Front-end side

- `site.loadModel(path)` (site-host.js, protocol v1.7) reads a `.glb` from
  `/media/` into plain arrays for WebGPU: meshes with positions, normals
  (generated when missing), uvs, Uint32 indices and a material (base colour,
  base-colour texture as an ImageBitmap, metallic, roughness, emissive, alpha
  mode, double-sided), with node transforms applied (mirrored nodes keep
  their winding), plus bounds and a triangle count. Rest pose only.

## Skills

`internal/builder/skills/<name>.md`, first line `description: ...`. Current:
`3d-models`, `generated-images`, `generated-video`, `textures`, `typography`, `webgpu-starter`. Code in a skill must
work as written: the 3d-models shader is compiled and rendered against the
Khronos sample models when it changes (see the live tests).

## Adding a connection

1. Implement `connect.Connection` in `internal/connect/<name>.go`. Return
   `connect.Userf(...)` errors with a message that tells the model what to do
   next; copy anything the front end loads into the store under `assets/`.
2. Add it in `connections()` in `cmd/server/main.go` (with its env/secret).
3. Unit-test it against an `httptest` fake (`connect_test.go` shows the
   pattern: `routeAll` sends every host to the fake); add a check to
   `live_test.go` (`go test -tags live ./internal/connect/`).
4. If it needs know-how, add a skill and mention it in the tool description.
