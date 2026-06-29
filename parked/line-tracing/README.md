# Parked: character line-tracing (camera-flies-along-letterforms)

Extracted from the live renderer on 2026-06-29. **Not compiled** — it lives
outside `src/` so Cargo/Trunk ignore it. Kept for possible future work.

## What this was

A section's title word was turned into an **ordered stroke-centerline path**, and
the "camera" (a scale-up of the title around its SDF) scrubbed *along* that path —
flying letter to letter as you scrolled, zooming to each stroke's width.

It was decided to be **too much, UX-wise**. We kept the swipe-to-section + the
word scale-up, and abandoned the path-following movement. This folder preserves the
interesting part (the path extraction) in case we revisit it.

## Contents

- `stroke_path.rs` — the algorithm, verbatim:
  - `zhang_suen()` — Zhang-Suen thinning (coverage → 1px skeleton).
  - `stroke_path_from()` — skeletonize, label letters, walk each in reading order
    (continue-straight at junctions, nearest restart at stroke ends), tag each
    point with stroke width. Returns flat `[x, y, width, …]`.

It depends on two helpers still in `src/lib.rs`: `edt8` (8SSEDT distance) and
`raster_title_sharp` (the anti-aliased coverage raster).

## How it was integrated (to revive)

- `bake_title()` returned `(path, sdf)`; the path was uploaded to JS as
  `window.__STROKE_PATH`.
- The frame loop scrubbed it: `title_t` (0..1 index along the path), `title_off_cur`
  eased toward the point (the "fly" across non-connected strokes), `title_scale_cur`
  eased toward `fill / stroke-width`. Dials: `fill`, `scrub`, `fly`.
- A hand-edit path editor: `paths.json` (baked) + `window.__PATH` / `__PATH_VER`
  (live) read by `read_path_override()`, seeded from `simplify_path()`, edited in
  the `↝` panel in `index.html` / `ui.js`.

The full, working integration is recoverable from git history — the commit just
before this parking removed all of the above from `src/lib.rs`, `ui.js`,
`index.html`, `dials.json`, and deleted `paths.json`.
