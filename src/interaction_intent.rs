//! interaction_intent — a home for intelligent interaction-handling utilities
//! that we grow over time (direction intent now; velocity/gesture intent, dwell,
//! long-press, etc. later). Each capability lives in its own submodule so a
//! component can opt into just the ones it needs.

pub mod drag_intent;
pub mod scroll_intent;
