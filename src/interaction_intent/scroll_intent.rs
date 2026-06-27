//! SCROLL INTENT — the wheel/trackpad sibling of
//! [`super::drag_intent::DragIntent`], built for functional parity with it.
//!
//! INERTIA is the whole problem here. A trackpad (and smooth-scroll mice) keep
//! firing `wheel` events with a long, decaying MOMENTUM tail for up to a second
//! after the fingers lift. If you scrub an offset straight from raw deltas the
//! gesture over-travels and never settles. So scroll is handled as PAGINATED
//! stepping, not position scrubbing:
//!
//!   • deltas accumulate into a running vector, snapped to a direction (same
//!     8-way / axis lock as a drag); between bursts the vector DECAYS rather than
//!     resetting, so deliberate notch-by-notch scrolling still adds up to a step
//!     while a stray notch long ago fades away;
//!   • once the accumulated travel along the axis crosses a threshold, emit ONE
//!     step (a unit direction toward the next detent) and enter a COOLDOWN;
//!   • during cooldown every same-direction delta (the momentum tail) is
//!     swallowed, so one flick = exactly one step. The cooldown lifts once the
//!     wheel goes quiet — or immediately if a clearly different (reverse or
//!     cross-axis) delta arrives, so you can change course without waiting out
//!     the inertia.
//!
//! OMNIDIRECTIONAL: both axes accumulate, so a 2-axis trackpad can step toward any
//! panel a drag can reach. DISABLED on touch devices ([`Self::set_enabled`]),
//! where the drag is the input and `wheel` is page scroll.

use std::f32::consts::FRAC_PI_4;

/// Collapse a direction to a single representative of its axis (line): prefer
/// +y, and +x on the x-axis, so opposite directions share one vector. (Mirrors
/// the helper in `drag_intent` so the two read the axis identically.)
fn canon_axis(d: (f32, f32)) -> (f32, f32) {
    if d.1 < -1e-3 || (d.1.abs() <= 1e-3 && d.0 < 0.0) {
        (-d.0, -d.1)
    } else {
        d
    }
}

/// One frame's result. `step` is `(0,0)` on most frames; on the frame a tick
/// fires it is the unit direction to move one detent toward (e.g. `(0,1)`).
pub struct ScrollOut {
    pub step: (f32, f32),
}

pub struct ScrollIntent {
    min_move: f32, // minimum accumulated travel before a direction is read
    // accumulated axis travel needed to fire one step (a fraction of the unit span
    // to a panel) — set live from the same FEEL dial the drag commit uses
    commit_threshold: f32,
    axis_mode: bool, // lock to an axis (bidirectional) instead of a single direction
    decay: f32,      // per-second decay of the accumulator between scroll bursts
    cooldown_idle: f32, // seconds of quiet before the post-step cooldown lifts
    enabled: bool,   // off on touch devices
    accum: (f32, f32),       // running accumulated travel
    cur: Option<(f32, f32)>, // current snapped direction (None until readable)
    cooldown: bool,          // swallowing the momentum tail after a step
    cool_t: f32,             // quiet time accrued during the cooldown
    last_step: (f32, f32),   // signed direction of the last step (to spot a course change)
}

impl ScrollIntent {
    pub fn new() -> Self {
        Self {
            min_move: 0.015,
            commit_threshold: 0.5,
            axis_mode: false,
            decay: 3.0,
            cooldown_idle: 0.18,
            enabled: true,
            accum: (0.0, 0.0),
            cur: None,
            cooldown: false,
            cool_t: 0.0,
            last_step: (0.0, 0.0),
        }
    }

    // --- mirror of DragIntent ------------------------------------------------

    /// Accumulated axis travel (fraction of the span to a panel) needed to fire a
    /// step. Mirrors [`super::drag_intent::DragIntent::set_commit_threshold`].
    pub fn set_commit_threshold(&mut self, fraction: f32) {
        self.commit_threshold = fraction;
    }

    /// The current commit threshold (fraction of span).
    #[allow(dead_code)]
    pub fn commit_threshold(&self) -> f32 {
        self.commit_threshold
    }

    /// Lock to an AXIS (a line) rather than a single 8-way direction. See
    /// [`super::drag_intent::DragIntent::set_axis_mode`].
    pub fn set_axis_mode(&mut self, on: bool) {
        self.axis_mode = on;
    }

    // --- scroll-specific -----------------------------------------------------

    /// Enable/disable the whole module. Disable on touch devices: while disabled
    /// [`Self::update`] ignores deltas and never steps.
    pub fn set_enabled(&mut self, on: bool) {
        if self.enabled && !on {
            self.reset();
        }
        self.enabled = on;
    }

    /// Whether scroll intent is enabled (true off touch, false on it).
    #[allow(dead_code)]
    pub fn enabled(&self) -> bool {
        self.enabled
    }

    /// Force every gesture/cooldown to end and clear state (e.g. when a drag
    /// takes over).
    pub fn reset(&mut self) {
        self.accum = (0.0, 0.0);
        self.cur = None;
        self.cooldown = false;
        self.cool_t = 0.0;
    }

    /// Feed this frame's accumulated scroll delta (already in the offset domain;
    /// pass `(0.0, 0.0)` on frames with no scroll). Returns whether travel is
    /// accumulating and, on the frame a tick fires, the unit direction to step.
    pub fn update(&mut self, delta: (f32, f32), dt: f32) -> ScrollOut {
        let none = ScrollOut { step: (0.0, 0.0) };
        if !self.enabled {
            return none;
        }
        let moved = (delta.0 * delta.0 + delta.1 * delta.1).sqrt();

        // COOLDOWN: swallow the momentum tail after a step. A delta still aligned
        // with the step is inertia → eat it. A clearly different one (reverse or
        // cross-axis) is a fresh intent → drop the cooldown and accumulate it.
        if self.cooldown {
            if moved > 1e-7 {
                let along = delta.0 * self.last_step.0 + delta.1 * self.last_step.1;
                if along >= 0.5 * moved {
                    self.cool_t = 0.0;
                    return none; // momentum (or a repeat in the same direction)
                }
                self.cooldown = false;
                self.accum = (0.0, 0.0); // course change — start clean
            } else {
                self.cool_t += dt;
                if self.cool_t >= self.cooldown_idle {
                    self.cooldown = false;
                    self.accum = (0.0, 0.0);
                }
                return none;
            }
        }

        // accumulate while scrolling; decay between bursts so partial progress
        // from deliberate notch-by-notch survives but stale scroll fades.
        if moved > 1e-7 {
            self.accum = (self.accum.0 + delta.0, self.accum.1 + delta.1);
        } else {
            let k = (-self.decay * dt).exp();
            self.accum = (self.accum.0 * k, self.accum.1 * k);
        }

        let m = (self.accum.0 * self.accum.0 + self.accum.1 * self.accum.1).sqrt();
        if m <= self.min_move {
            self.cur = None;
            return ScrollOut { step: (0.0, 0.0) }; // not readable yet
        }
        let a = (self.accum.1.atan2(self.accum.0) / FRAC_PI_4).round() * FRAC_PI_4;
        let mut dir = (a.cos(), a.sin());
        if self.axis_mode {
            dir = canon_axis(dir);
        }
        self.cur = Some(dir);

        let proj = self.accum.0 * dir.0 + self.accum.1 * dir.1; // travel along the axis
        if proj.abs() >= self.commit_threshold {
            // crossed the threshold → one step toward the scrolled direction, then
            // cool down to absorb the inertia that follows.
            let step = if proj >= 0.0 { dir } else { (-dir.0, -dir.1) };
            self.last_step = step;
            self.cooldown = true;
            self.cool_t = 0.0;
            self.accum = (0.0, 0.0);
            self.cur = None;
            return ScrollOut { step };
        }
        ScrollOut { step: (0.0, 0.0) }
    }

    /// Is travel currently accumulating?
    #[allow(dead_code)]
    pub fn active(&self) -> bool {
        self.cur.is_some()
    }

    /// The direction in effect this frame, or None before any intent is read.
    #[allow(dead_code)]
    pub fn direction(&self) -> Option<(f32, f32)> {
        self.cur
    }
}
