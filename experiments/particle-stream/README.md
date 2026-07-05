# personal-site

**Words as rocks in a stream.** 380,000 WebGPU compute-shader particles flow
left→right across the screen; the text ("BEN PRIDDY" + a cycling phrase) is not
a cluster target but an *obstacle field* — the stream parts around the
letterforms like water around rocks, accumulating and sparkling at the upstream
faces like sun on water at noon. Runs at 120 FPS.

```
Rust  →  wasm32  →  wgpu (compute + render)  →  WebGPU  →  <canvas>
```

## How it works

- **Obstacle field** — both text lines are rasterized to a hidden 2D canvas
  (wide blur halo + tight core), and the red channel is uploaded as an
  `r8unorm` texture. The compute shader samples it and deflects particles along
  its gradient: away-push + tangential slide, so flow hugs the glyph surfaces.
  Line 2 cycles every 4.5 s through a word-chain of phrases; each swap drops a
  new "rock" into the stream and the particles physically react.
- **Noon sparkle** — sparkle is gated on *stagnation* (speed deficit vs. the
  stream), so glints concentrate exactly where particles accumulate: bow waves
  and the trapped pools inside letter counters.
- **Color** — each particle's flow direction is encoded exactly like a normal
  map's RG channels with the blue (flat/z) channel suppressed: the warm rim
  palette. Rightward flow is salmon-gold, upward deflection lime, downward
  crimson — so the parting flow paints itself.
- **Motion stretch** — quads are stretched along velocity: fast water becomes
  silky streamlines, stalled water stays round and glints.
- **Mouse** — a gentle radial push, a finger dragged through the stream.
- **Backdrop** — a dim normal-mapped riverbed that the glyph field embosses
  (darkened fill + warm rim), so the words also read faintly in the bed.
- **Info** — the ⓘ button (bottom right) opens a modal with a short bio
  (placeholder copy).

Particle state lives entirely on the GPU (storage buffer; compute integrates,
render reads the same buffer as instanced vertex data). The only per-frame CPU
work is a 48-byte uniform write.

## Run

Needs the Rust toolchain + `wasm32-unknown-unknown` + `trunk`.

```sh
trunk serve
# open http://127.0.0.1:8099  (foreground tab for full FPS)
```

## Deploy

CI/CD via GitHub Actions (`.github/workflows/deploy.yml`): every push to
`main` runs `trunk build --release` and publishes `dist/` to GitHub Pages.
The workflow feeds Pages' `base_path` to trunk's `--public-url`, so it works
at a project URL (`user.github.io/repo/`) or a custom domain unchanged.
One-time setup after pushing to GitHub: repo Settings → Pages → Source =
"GitHub Actions". Release size: ~240 KB total (wasm + js + html).

Note: WebGPU requires HTTPS (Pages provides it) and a current browser;
unsupported browsers see the status chip's adapter message.

## Files

| File | Role |
|---|---|
| `src/lib.rs` | field rasterizer, wgpu setup, sim compute + 2 render pipelines, WGSL |
| `index.html` | canvas, GPU-error debug shim, info modal, styling |
| `Cargo.toml` | `cdylib`; `wgpu` pinned current |
| `Trunk.toml` | `[build] target = "index.html"` |

## Tuning

In `src/lib.rs`: `PARTICLES`, `PHRASE_SECONDS`, the `PHRASES` list, and the
per-frame `Params` (`stream`, `push`, `mousef`). In the draw shader: sparkle
gain, motion-stretch length, palette weights.

## Hard-won notes

- `target` is a **reserved word in WGSL** — using it as a variable name
  invalidates the compute pipeline, which poisons the whole command buffer, and
  the screen stays black with *zero* console errors.
- WebGPU errors are devtools-channel messages; `index.html` carries a small
  shim that re-emits `uncapturederror` through `console.error` so they're
  visible to tooling.
