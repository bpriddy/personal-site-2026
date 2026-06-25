//! DIRECTION INTENT for drags.
//!
//! After a gesture begins (touchstart / mousedown+move), the dominant 8-way
//! direction is sampled over a short window and then LOCKED. From then until the
//! gesture ends, only the component of motion *along* that locked axis is fed
//! back — so if you start dragging down and then wander diagonally, only the
//! downward part of that diagonal counts. Erratic mid-drag motion can't
//! destabilize the tracked direction.
//!
//! In AXIS mode (see [`DragIntent::set_axis_mode`]) the lock collapses to an
//! axis (line) rather than a single direction (ray): "dragging down" and
//! "dragging up" both lock the *y axis*, so motion in BOTH directions along it
//! is valid input. The locked vector is one canonical representative of the
//! axis; the sign of the projection tells which way you're going.

use std::f32::consts::FRAC_PI_4;

/// Collapse a direction to a single representative of its axis (line): prefer
/// +y, and +x on the x-axis, so opposite directions share one vector.
fn canon_axis(d: (f32, f32)) -> (f32, f32) {
    if d.1 < -1e-3 || (d.1.abs() <= 1e-3 && d.0 < 0.0) {
        (-d.0, -d.1)
    } else {
        d
    }
}

pub struct DragIntent {
    window: f32,   // seconds of sampling before the direction locks
    min_move: f32, // minimum displacement (field-UV) before a direction is read
    // fraction of a caller-supplied span the locked-axis travel must exceed for the
    // gesture to read as intent to reach the far state (vs. fall back to the near one)
    commit_threshold: f32,
    axis_mode: bool, // lock to an axis (bidirectional) instead of a single direction
    active: bool,
    elapsed: f32,
    base: (f32, f32),           // tracked value at gesture start
    locked: Option<(f32, f32)>, // frozen 8-way unit direction (after the window)
    cur: Option<(f32, f32)>,    // direction in effect this frame (provisional or locked)
}

impl DragIntent {
    pub fn new() -> Self {
        Self {
            window: 0.12,
            min_move: 0.015,
            commit_threshold: 0.5,
            axis_mode: false,
            active: false,
            elapsed: 0.0,
            base: (0.0, 0.0),
            locked: None,
            cur: None,
        }
    }

    /// How far the drag must travel along its locked axis — as a fraction of the
    /// span to the off-screen item — before the gesture reads as intent to reach
    /// that item rather than snap back to rest. Lower commits with less drag.
    /// (Default 0.5 = halfway.)
    pub fn set_commit_threshold(&mut self, fraction: f32) {
        self.commit_threshold = fraction;
    }

    /// The current commit threshold (fraction of span). See [`Self::set_commit_threshold`].
    pub fn commit_threshold(&self) -> f32 {
        self.commit_threshold
    }

    /// Lock to an AXIS (a line) rather than a single 8-way direction. With this
    /// on, dragging up vs. down (or left vs. right, or either way along a
    /// diagonal) locks the SAME axis, and motion in both directions along it is
    /// valid input. The locked/returned vector is the axis's canonical
    /// representative; read the sign of the offset to know which way.
    pub fn set_axis_mode(&mut self, on: bool) {
        self.axis_mode = on;
    }

    /// Start a gesture. `base` is the tracked value (here: the text offset) at press.
    pub fn begin(&mut self, base: (f32, f32)) {
        self.active = true;
        self.elapsed = 0.0;
        self.base = base;
        self.locked = None;
        self.cur = None;
    }

    /// Feed the raw (unconstrained) target each frame; returns the
    /// direction-stabilized target. Before enough movement is read it tracks the
    /// raw input; once moving it projects onto the dominant 8-way axis, and after
    /// the sampling window that axis is locked for the rest of the gesture.
    pub fn update(&mut self, raw: (f32, f32), dt: f32) -> (f32, f32) {
        if !self.active {
            return raw;
        }
        self.elapsed += dt;
        let d = (raw.0 - self.base.0, raw.1 - self.base.1);
        let dir = match self.locked {
            Some(l) => l,
            None => {
                let m = (d.0 * d.0 + d.1 * d.1).sqrt();
                if m <= self.min_move {
                    self.cur = None;
                    return raw; // intent not readable yet — follow the finger
                }
                let a = (d.1.atan2(d.0) / FRAC_PI_4).round() * FRAC_PI_4;
                let mut prov = (a.cos(), a.sin());
                if self.axis_mode {
                    prov = canon_axis(prov); // collapse the direction to its axis
                }
                if self.elapsed >= self.window {
                    self.locked = Some(prov); // window elapsed → lock the dominant axis
                }
                prov
            }
        };
        self.cur = Some(dir);
        let proj = d.0 * dir.0 + d.1 * dir.1; // motion ALONG the axis only
        (self.base.0 + dir.0 * proj, self.base.1 + dir.1 * proj)
    }

    /// End the gesture.
    pub fn end(&mut self) {
        self.active = false;
    }

    /// The direction in effect this frame (provisional during the window, then
    /// the locked axis), or None before any intent is read.
    pub fn direction(&self) -> Option<(f32, f32)> {
        self.cur
    }

    /// The committed axis, available only once the sampling window has locked it
    /// (None during the window). Use this to react once per gesture rather than
    /// to the provisional direction that may still be settling.
    #[allow(dead_code)]
    pub fn locked_direction(&self) -> Option<(f32, f32)> {
        self.locked
    }
}
