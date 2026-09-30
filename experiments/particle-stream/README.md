# particle-stream

**Words as rocks in a stream.** Up to 500,000 WebGPU compute-shader particles
flow across the screen; a centered, cycling phrase is not a cluster target but
an *obstacle field* — the stream parts around the letterforms like water around
rocks, accumulating and sparkling at the upstream faces like sun on water at
noon.

```
Rust  →  wasm32  →  wgpu (compute + render)  →  WebGPU  →  <canvas>
```

This is the first experiment in the monorepo (see the root `README.md`).

## How it works

- **Obstacle field** — the phrase is rasterized to a hidden 2D canvas (wide blur
  halo + tight core) and uploaded as a texture. The compute shader deflects
  particles along its gradient: away-push + tangential slide, so flow hugs the
  glyph surfaces. The phrase cycles every 4.5 s (`PHRASE_SECONDS`) with a fade +
  z-push transition; each swap drops a new "rock" into the stream.
- **Phrases** — `phrases.json`, a plain JSON string array, word-wrapped by
  measured pixel width. Baked into the wasm at build time via `include_str!`.
- **Noon sparkle** — sparkle is gated on *stagnation* (speed deficit vs. the
  stream), so glints concentrate in bow waves and letter counters.
- **Color** — each particle's flow direction is encoded like a normal map's RG
  channels: rightward flow salmon-gold, upward lime, downward crimson.
- **HDR pipeline** — bloom on the particles; the text relief is composited
  above the bloom so its edges stay crisp.
- **Mouse / touch** — perturbs the stream by position; pressing adds a radial
  wake around the words.
- **Intro** — a one-shot inflow animation on load.
- **Info** — the ⓘ button (bottom right) opens a bio modal.

Particle state lives entirely on the GPU; the per-frame CPU work is a uniform
write. Desktop runs 500k particles, viewports under 700px wide run 200k.

## Run

Needs the Rust toolchain + `wasm32-unknown-unknown` + `trunk` (v0.21.14).

```sh
cd experiments/particle-stream
trunk serve
# open http://127.0.0.1:8099  (foreground tab for full FPS)
```

## As a site front end (`builtin/particle-stream`)

The site can show this piece as its front end, in a sandboxed iframe on the
user-content origin (see `docs/frontend-protocol.md`). Build the bundle from
the repo root:

```sh
scripts/build-frontends.sh   # → build/frontends/builtin/particle-stream/ (and builtin/site/)
```

That runs `trunk build --release --public-url ./` (relative asset paths).
`index.html` loads the host API `/site-host.js` before the app; the app uses
it only if `window.site` exists, so `trunk serve` still runs standalone:

- `site.ready()` after the first frame is presented;
- `site.reportError(…, "error")` if there is no WebGPU adapter,
  `"gpu-lost"` if `requestDevice` fails or the device is lost;
- the site content (`site.loaded`, `site.onRoute`) is ignored for now: the
  piece still shows its own `phrases.json`.

Embedded, the page runs in an opaque origin: all `localStorage` /
`indexedDB` / clipboard use is guarded, the phrase SAVE can't write a file,
and the page doesn't reload on resize (the signed index URL expires after
60s); the canvas stretches instead.

## Dev tooling

Visible when run standalone on `localhost` / `127.0.0.1`; hidden when
deployed or embedded in the site (the keys still toggle the panels):

| Key | Panel |
|---|---|
| `d` | FLOW DIALS — stream, turbulence, sparkle, background, perturb |
| `f` | FEEL DIALS — wake, porosity, plus dials for parked features |
| `p` | PHRASES — edit and save `phrases.json` in place (File System Access API) |

`dials.json` is the single source of truth for tuned defaults: it is embedded
at build time, and the dial panels' SAVE button downloads a new copy to commit.
Some keys (`name_lead`, `commit`, `menu_lerp`, `scroll`, `entry_slide`,
`entry_zoom`) belong to parked features and are kept for when they return.

## Deploy

The Go container site serves this at `/experiments/particle-stream/`; the root
`Dockerfile` builds it into the Cloud Run image, and `make experiments` builds
it for local serving. To build by hand:

```sh
cd experiments/particle-stream
trunk build --release                                   # served at a domain root
trunk build --release --public-url /experiments/particle-stream/   # under that path
trunk build --release --public-url ./                   # relative (site front end)
```

Output is static files in `dist/` (~240 KB).

WebGPU requires HTTPS and a current browser; unsupported browsers see the
status chip's adapter message.

## Files

| File | Role |
|---|---|
| `src/lib.rs` | field rasterizer, wgpu setup, sim compute + render + post pipelines, WGSL |
| `index.html` | canvas, GPU-error debug shim, info modal, dev panels, styling |
| `ui.js` | modal, dial panels, phrase editor |
| `dials.json` | tuned dial defaults (build-time baked) |
| `phrases.json` | the cycling phrases (build-time baked) |
| `parked/` | shelved features, not compiled — see each folder's README |
| `Cargo.toml` / `Trunk.toml` | `cdylib` crate; trunk targets `index.html` |

## Parked features

- `parked/drag-nav/` — draggable/throwable text, menu panels, section nav,
  scroll intent.
- `parked/line-tracing/` — camera flying along title letterforms.

## Hard-won notes

- `target` is a **reserved word in WGSL** — using it as a variable name
  invalidates the compute pipeline, which poisons the whole command buffer, and
  the screen stays black with *zero* console errors.
- WebGPU errors are devtools-channel messages; `index.html` carries a small
  shim that re-emits `uncapturederror` through `console.error` so they're
  visible to tooling.
