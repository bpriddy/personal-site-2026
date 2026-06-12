# personal-site

GPU particle text. **250,000 WebGPU compute-shader particles** swarm to attraction
points sampled from text geometry ("BEN PRIDDY"), over a normal-map lit backdrop
that adds a secondary color/light layer. Runs at 120 FPS.

```
Rust  →  wasm32  →  wgpu (compute + render)  →  WebGPU  →  <canvas>
```

Particle `pos`/`vel` live in a GPU storage buffer; a compute shader integrates
them each frame (spring-to-target + curl jitter); the render pass reads the same
buffer as vertices and draws additive points — nothing round-trips to the CPU.

## Run

Needs the Rust toolchain + `wasm32-unknown-unknown` + `trunk`.

```sh
trunk serve
# open http://127.0.0.1:8099  (foreground tab for full FPS)
```

## How it works

| File | Role |
|---|---|
| `src/lib.rs` | text sampling, wgpu setup, compute + 2 render pipelines, the WGSL |
| `index.html` | `<canvas>` + the trunk rust directive |
| `Cargo.toml` | `cdylib`; `wgpu` pinned to a current version |
| `Trunk.toml` | `[build] target = "index.html"` |

**Text → attraction points:** "BEN PRIDDY" is rasterized to a hidden 2D canvas;
opaque pixels become NDC target points (aspect-corrected). Particles are assigned
`target[i % N]` and spring toward it.

## Tuning

In `src/lib.rs`: `PARTICLES` (density), and the `spring` / `damp` / `noise`
constants in the per-frame `Params` (lower spring + higher noise → more flow and
orbit instead of locking tight to the glyphs). Built and verified with the
`wasm-renderer` Claude Code skill.
