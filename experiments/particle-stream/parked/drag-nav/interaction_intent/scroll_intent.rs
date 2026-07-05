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
//!     8-way / axis lock as a drag). Partial progress is kept across the gaps
//!     between deliberate notches and only FORGOTTEN after a quiet stretch
//!     (`forget_timeout`), so notch-by-notch on a mouse wheel adds up to a step
//!     while a stray notch from long ago fades;
//!   • once the accumulated travel along the axis crosses `step_threshold`, emit
//!     ONE step (a unit direction toward the next detent) and enter a COOLDOWN;
//!   • during cooldown EVERY delta is swallowed as inertia, so one flick = exactly
//!     one step — no matter how the momentum tail wobbles off-axis. The cooldown
//!     lifts once the wheel goes quiet, or immediately on a clear REVERSAL (a
//!     delta pointing back against the step), so you can change course without
//!     waiting out the inertia.
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
    // accumulated axis travel (in the scroll's own UV domain, NOT the drag's
    // fraction-of-span) needed to fire one step
    step_threshold: f32,
    axis_mode: bool,    // lock to an axis (bidirectional) instead of a single direction
    forget_timeout: f32, // quiet seconds before partial accumulation is dropped
    cooldown_idle: f32,  // quiet seconds before the post-step cooldown lifts
    enabled: bool,       // off on touch devices
    accum: (f32, f32),       // running accumulated travel
    cur: Option<(f32, f32)>, // current snapped direction (None until readable)
    idle: f32,               // quiet time accrued while accumulating
    cooldown: bool,          // swallowing the momentum tail after a step
    cool_t: f32,             // quiet time accrued during the cooldown
    last_step: (f32, f32),   // signed direction of the last step (to spot a reversal)
}

impl ScrollIntent {
    pub fn new() -> Self {
        Self {
            min_move: 0.015,
            step_threshold: 0.2,
            axis_mode: false,
            forget_timeout: 1.0,
            cooldown_idle: 0.18,
            enabled: true,
            accum: (0.0, 0.0),
            cur: None,
            idle: 0.0,
            cooldown: false,
            cool_t: 0.0,
            last_step: (0.0, 0.0),
        }
    }

    // --- mirror of DragIntent ------------------------------------------------

    /// Accumulated travel (in scroll UV) needed to fire a step. Named to mirror
    /// [`super::drag_intent::DragIntent::set_commit_threshold`], but note the
    /// scroll threshold lives in a DIFFERENT domain than the drag's fraction-of-
    /// span, so it is NOT driven from the drag's commit dial.
    #[allow(dead_code)]
    pub fn set_commit_threshold(&mut self, travel: f32) {
        self.step_threshold = travel;
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
        self.idle = 0.0;
        self.cooldown = false;
        self.cool_t = 0.0;
    }

    /// Feed this frame's accumulated scroll delta (already in the offset domain;
    /// pass `(0.0, 0.0)` on frames with no scroll). On the frame a tick fires,
    /// the returned `step` is the unit direction to move one detent toward.
    pub fn update(&mut self, delta: (f32, f32), dt: f32) -> ScrollOut {
        let none = ScrollOut { step: (0.0, 0.0) };
        if !self.enabled {
            return none;
        }
        let moved = (delta.0 * delta.0 + delta.1 * delta.1).sqrt();

        // COOLDOWN: after a step, swallow the inertia tail. EVERY delta is eaten as
        // momentum (however much it wobbles off-axis) — only a clear REVERSAL (a
        // delta pointing back against the step) ends the cooldown, and even then we
        // consume that delta here so it can never seed a fresh step on the same frame.
        if self.cooldown {
            if moved > 1e-7 {
                let along = delta.0 * self.last_step.0 + delta.1 * self.last_step.1;
                if along < -0.25 * moved {
                    self.reset(); // a real course reversal — start clean next frame
                } else {
                    self.cool_t = 0.0; // momentum still flowing
                }
                return none;
            }
            self.cool_t += dt;
            if self.cool_t >= self.cooldown_idle {
                self.cooldown = false;
                self.accum = (0.0, 0.0);
                self.cur = None;
            }
            return none;
        }

        // accumulate while scrolling; keep partial progress across the gaps between
        // deliberate notches, but forget it after a quiet stretch.
        if moved > 1e-7 {
            self.accum = (self.accum.0 + delta.0, self.accum.1 + delta.1);
            self.idle = 0.0;
        } else {
            self.idle += dt;
            if self.idle >= self.forget_timeout {
                self.accum = (0.0, 0.0);
                self.cur = None;
            }
            return none;
        }

        let m = (self.accum.0 * self.accum.0 + self.accum.1 * self.accum.1).sqrt();
        if m <= self.min_move {
            self.cur = None;
            return none; // not readable yet
        }
        let a = (self.accum.1.atan2(self.accum.0) / FRAC_PI_4).round() * FRAC_PI_4;
        let mut dir = (a.cos(), a.sin());
        if self.axis_mode {
            dir = canon_axis(dir);
        }
        self.cur = Some(dir);

        let proj = self.accum.0 * dir.0 + self.accum.1 * dir.1; // travel along the axis
        if proj.abs() >= self.step_threshold {
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
        none
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
