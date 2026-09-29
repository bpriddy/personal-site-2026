# Parked: drag + panel/section navigation

Removed from the live renderer on 2026-07-02 (commit `94a81fe`, "Restructure
step 1"). **Not compiled** — it lives outside `src/` so Cargo/Trunk ignore it.
Kept because we will likely revisit it.

## What this was

- **Draggable text** — grab the words, throw them; they settle with friction
  and plow the particle field (bow wave / wake, snow-plow displacement with
  anti-tunnel probing so fast throws don't skim through particles).
- **Menu panels** — a drag conveyor with fixed compass words (MENU = north),
  clamped single-axis drag revealing panels in both directions.
- **Section nav** — swiping into a section slid the main text over and scaled
  up that section's title, with a 2-phase eased entry animation.
- **Scroll intent** — wheel/trackpad handling with paginated stepping to absorb
  trackpad inertia.

The site was pared back to a bare field + cycling phrase driven only by
mouse/touch perturbation.

## Contents

- `interaction_intent.rs` — module root declaring the two submodules below.
- `interaction_intent/drag_intent.rs` — `DragIntent`: drag direction intent,
  8-direction snap, snap-to-dominant axis.
- `interaction_intent/scroll_intent.rs` — `ScrollIntent`: paginated wheel /
  trackpad stepping, line-notch detents, forget window.
- `lib.rs.snapshot` — the full renderer just before the strip, with all the
  gesture, panel and section code wired in.

## Still in the live code (inert)

The shaders, `Params` and `dials.json` still carry the drag/panel/section
fields (fed neutral values), and the FEEL DIALS panel still shows their dials.
This is deliberate, to make reviving easier.

## To revive

1. Move `interaction_intent.rs` and `interaction_intent/` back into `src/`, and
   add `mod interaction_intent;` to `src/lib.rs`.
2. Port the gesture block, listeners and state from `lib.rs.snapshot` (diff it
   against the current `src/lib.rs`; the phrase layout and name removal changed
   since). Full history is also recoverable from `94a81fe^`.
3. Re-add `WheelEvent` handling (the web-sys feature is still enabled).
