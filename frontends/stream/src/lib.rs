use std::cell::{Cell, RefCell};
use std::rc::Rc;
use wasm_bindgen::prelude::*;
use wasm_bindgen::JsCast;


// ─────────────────────────────────────────────────────────────────────────────
// "Words as rocks in a stream" — HDR edition.
//
// A dense GPU-compute particle stream flows across the screen; the two text
// lines are obstacles (blurred field channel R drives deflection), while a
// SECOND, sharp channel (G) paints the same words as crisp white type. The
// scene — bright screen-space normal-map background, sharp white text, additive
// light particles — renders into an HDR (rgba16float) buffer, then a bloom
// chain (bright-extract → separable blur at half res) and a filmic tonemap
// composite it to the surface, so sparkle glints genuinely glow hot.
// ─────────────────────────────────────────────────────────────────────────────

// The cycling phrases live in phrases.json (a plain JSON string array, baked at build
// time, editable live via the hidden ✎ panel → window.__PHRASES). Each string is a
// line that cycles (centered); PHRASE_SECONDS sets the cycle.
const PHRASE_SECONDS: f64 = 4.5;
// section entry: a swipe-left TRIGGERS an automatic 2-phase eased animation —
// slide the word in from the right (0..ENTRY_T1), then zoom into the path start
// (ENTRY_T1..ENTRY_TOT). Seconds.
const ENTRY_T1: f32 = 0.35;
const ENTRY_TOT: f32 = 0.78;

const PARTICLES: u32 = 500_000;
const WG: u32 = 64;
const FIELD_W: u32 = 1536; // wider = crisper sharp-text channel

#[repr(C)]
#[derive(Clone, Copy, bytemuck::Pod, bytemuck::Zeroable)]
struct Params {
    res: [f32; 2],
    mouse: [f32; 2],
    time: f32,
    dt: f32,
    count: u32,
    stream: f32,
    push: f32,
    mousef: f32,
    dpr: f32,
    rot_speed: f32,
    rot_depth: f32,
    turb: f32,
    eddy: f32,
    sparkg: f32,
    bg_freq: f32,
    text_sat: f32,
    bg_speed: f32,
    mobile: f32,
    phrase_w: f32,  // phrase obstacle strength (physics) — 0 when receded
    phrase_op: f32, // phrase visual opacity
    phrase_z: f32,  // phrase visual z-scale (1=resting, <1=pushed back)
    phrase_cy: f32, // phrase block center (uv.y) — the z-scale pivot
    bg_fade: f32,
    part_fade: f32,
    name_op: f32,
    intro_glow: f32,
    text_du: f32, // text drag offset in field-UV
    text_dv: f32,
    text_vx: f32, // text travel velocity (NDC/s) - plows the field
    text_vy: f32,
    menu_du: f32, // MENU conveyor offset (field-UV)
    menu_dv: f32,
    pad0: f32, // active panel cell centre (atlas-uv) for the reveal mask
    pad1: f32,
    wake: f32,     // plow/wake strength (live FEEL dial)
    porosity: f32, // rest-state flow-through between glyphs (live FEEL dial)
    pressed: f32,    // 1.0 while a finger/mouse is down (mousedown..mouseup)
    wake_width: f32, // press-wake berth radius (fraction of screen width, live dial)
    press_z: f32,    // visible text z-scale: <1 recedes on press, eases back on release
    menu_vx: f32,    // off-screen panel velocity (NDC/s, no name_lead) for its plow
    menu_vy: f32,
    bump: f32, // CAMERA wall-push strength: swipe shoves particles into the revealed area
}

// Compute: integrate particles against the obstacle field (channel R).
const SIM_SHADER: &str = r#"
struct Particle { pos: vec2<f32>, vel: vec2<f32> };
struct Params {
  res: vec2<f32>, mouse: vec2<f32>,
  time: f32, dt: f32, count: u32, stream: f32,
  push: f32, mousef: f32, dpr: f32, rot_speed: f32,
  rot_depth: f32, turb: f32, eddy: f32, sparkg: f32,
  bg_freq: f32, text_sat: f32, bg_speed: f32, mobile: f32,
  phrase_w: f32, phrase_op: f32, phrase_z: f32, phrase_cy: f32,
  bg_fade: f32, part_fade: f32, name_op: f32, intro_glow: f32,
  text_du: f32, text_dv: f32, text_vx: f32, text_vy: f32,
  menu_du: f32, menu_dv: f32, pad0: f32, pad1: f32,
  wake: f32, porosity: f32, pressed: f32, wake_width: f32,
  press_z: f32, menu_vx: f32, menu_vy: f32, bump: f32,
};
@group(0) @binding(0) var<uniform> P: Params;
@group(0) @binding(1) var field: texture_2d<f32>;
@group(0) @binding(2) var fsamp: sampler;
@group(0) @binding(3) var menu: texture_2d<f32>;
@group(0) @binding(4) var sdftex: texture_2d<f32>; // wake distance field (RG=dir, B=dist)
@group(0) @binding(5) var menusdf: texture_2d<f32>; // off-screen panel wake field
@group(1) @binding(0) var<storage, read_write> parts: array<Particle>;
// section-title transform (own group): x=scale, yz=offset(title uv), w=on
@group(2) @binding(0) var<uniform> titlex: vec4<f32>;
@group(2) @binding(1) var title_sdf: texture_2d<f32>;
@group(2) @binding(2) var title_samp: sampler;

fn pcg(v: u32) -> u32 {
  var s = v * 747796405u + 2891336453u;
  s = ((s >> ((s >> 28u) + 4u)) ^ s) * 277803737u;
  return (s >> 22u) ^ s;
}
fn rand01(v: u32) -> f32 { return f32(pcg(v)) / 4294967295.0; }

fn fieldAt(p: vec2<f32>) -> f32 {
  let uv = vec2<f32>(p.x * 0.5 + 0.5 - P.text_du, 0.5 - p.y * 0.5 - P.text_dv);
  let s = textureSampleLevel(field, fsamp, uv, 0.0);
  // the chrome/phrase layer (BA) stays put; only the page layer (RG) slides
  let c = textureSampleLevel(field, fsamp, vec2<f32>(p.x * 0.5 + 0.5, 0.5 - p.y * 0.5), 0.0);
  let muv = (vec2<f32>(p.x * 0.5 + 0.5, 0.5 - p.y * 0.5) + vec2<f32>(1.0, 1.0) - vec2<f32>(P.menu_du, P.menu_dv)) / 3.0;
  // only the single active panel cell (pad0,pad1) contributes - never its neighbours
  let inCell = abs(muv.x - P.pad0) < 0.1667 && abs(muv.y - P.pad1) < 0.1667;
  let mr = select(0.0, textureSampleLevel(menu, fsamp, muv, 0.0).r, inCell);
  // name (R) permanent; phrase (B) fades with z; panel (mr) pans in opposite the
  // drag — suppressed as a section title takes over (titlex.w) so the small east
  // panel's obstacle doesn't ghost behind the big zoomed title.
  return max(max(s.r * P.name_op, c.b * P.phrase_w), mr * (1.0 - titlex.w));
}

@compute @workgroup_size(64)
fn cs(@builtin(global_invocation_id) gid: vec3<u32>) {
  let i = gid.x;
  if (i >= P.count) { return; }
  var pt = parts[i];
  let h1 = rand01(i);
  let h2 = rand01(i ^ 0x9e3779b9u);

  // ── flow field: rotating origin + turbulence + roaming eddies ──
  // the global heading does a slow bounded noise-walk (two incommensurate
  // sines), so the flow's ORIGIN itself rotates around the screen; the dials
  // control how fast (rot_speed) and how far (rot_depth) it swings
  let th = P.rot_depth * (0.6 * sin(P.time * P.rot_speed)
                        + 0.4 * sin(P.time * P.rot_speed * 0.371 + 2.1));
  // micro-wobble layered on top: the current still breathes locally
  let m_ang = th + 0.16 * sin(P.time * 0.11 + pt.pos.y * 0.6)
            + 0.09 * sin(P.time * 0.047 + 1.7);
  let mdir = vec2<f32>(cos(m_ang), sin(m_ang));
  let lane = 0.5 + 0.5 * sin(pt.pos.y * 7.0 + h1 * 6.2832);
  let goal = mdir * (P.stream * (0.55 + 0.9 * lane));
  var v = pt.vel + (goal - pt.vel) * min(2.6 * P.dt, 1.0);

  // CAMERA WALL-PUSH: a swipe pans the camera over the world; shove the particles
  // toward the newly revealed area (OPPOSITE the camera's travel velocity — the words
  // slide off one edge, so the fresh region is the other edge) so the stream FLOODS in
  // like it's being bumped by a wall, instead of arriving empty. Scales with the camera
  // speed (text_vx/vy), so it only acts while panning and settles once the pan stops.
  v -= vec2<f32>(P.text_vx, P.text_vy) * P.bump;

  // three octaves of drifting pseudo-curl: broad swells, mid eddy-chop, shimmer
  v += vec2<f32>(
    sin(pt.pos.y * 3.0 + P.time * 0.50 + h2 * 6.2832),
    cos(pt.pos.x * 2.5 - P.time * 0.40 + h1 * 6.2832)
  ) * 0.16 * P.turb * P.dt;
  v += vec2<f32>(
    sin(pt.pos.y * 9.0 - P.time * 1.10 + h1 * 2.1),
    cos(pt.pos.x * 8.0 + P.time * 0.90 + h2 * 4.2)
  ) * 0.10 * P.turb * P.dt;
  v += vec2<f32>(
    sin(pt.pos.y * 21.0 + P.time * 2.30 + h2 * 9.1),
    cos(pt.pos.x * 19.0 - P.time * 2.00 + h1 * 7.3)
  ) * 0.055 * P.turb * P.dt;

  // roaming eddies: three slow vortices drift through and stir the stream
  for (var k = 0u; k < 3u; k = k + 1u) {
    let fk = f32(k);
    let ph = fk * 2.094;
    let c = vec2<f32>(
      0.85 * sin(P.time * (0.061 + fk * 0.013) + ph),
      0.62 * cos(P.time * (0.043 + fk * 0.017) + ph * 1.3)
    );
    let d = pt.pos - c;
    let r2 = dot(d, d);
    var w = 0.9;
    if ((k & 1u) == 1u) { w = -0.75; }
    v += vec2<f32>(-d.y, d.x) * w * P.eddy * exp(-r2 * 5.0) * P.dt;
  }

  // obstacle deflection: push away from glyphs along the field gradient
  let f = fieldAt(pt.pos);
  if (f > 0.02) {
    // 0 at rest, ->1 while engaged. At rest the glyphs are porous so particles
    // thread BETWEEN the letters; pressing or moving makes them shove hard.
    let dragk = max(clamp(length(vec2<f32>(P.text_vx, P.text_vy)) * 0.5, 0.0, 1.0), P.pressed);
    // porosity opens the glyphs at rest (full deflection returns while dragging)
    let pf = (1.0 - P.porosity) + P.porosity * dragk;
    let e = 0.012;
    let gx = fieldAt(pt.pos + vec2<f32>(e, 0.0)) - fieldAt(pt.pos - vec2<f32>(e, 0.0));
    let gy = fieldAt(pt.pos + vec2<f32>(0.0, e)) - fieldAt(pt.pos - vec2<f32>(0.0, e));
    let g = vec2<f32>(gx, gy);
    let gl = length(g);
    if (gl > 1e-5) {
      let n = g / gl;
      v -= n * P.push * (f * f * 4.0 + f * 0.6) * pf * P.dt;
      let into = dot(v, n);
      if (into > 0.0) { v -= n * into * min(8.0 * f * P.dt, 0.9) * pf; }
    }
    // a moving word plows the field along its travel - bow wave + wake, like an
    // object dragged through water. sqrt(f) term widens the wake into the halo.
    v += vec2<f32>(P.text_vx, P.text_vy) * (f * 6.0 + sqrt(f) * 3.0) * P.wake * P.dt;
  }
  // the wake stays engaged while the word block is DOWN *or* still moving, so a
  // throw keeps displacing particles until the settle animation actually finishes
  let engaged = P.pressed > 0.5 || length(vec2<f32>(P.text_vx, P.text_vy)) > 0.05;
  // PRESS WAKE: push a soft even berth around the words (steady, baked SDF: RG =
  // screen-outward unit dir, B = distance). This handles the STATIC clear; the
  // moving-word "plow" is a position SNAP-TO-EDGE applied after integration below.
  if (engaged) {
    let suv = vec2<f32>(pt.pos.x * 0.5 + 0.5, 0.5 - pt.pos.y * 0.5)
              - vec2<f32>(P.text_du, P.text_dv);
    let suv0 = vec2<f32>(pt.pos.x * 0.5 + 0.5, 0.5 - pt.pos.y * 0.5);
    let sdf = textureSampleLevel(sdftex, fsamp, suv, 0.0);
    let dist = sdf.b * 0.35; // decode (matches bake maxdist)
    if (dist < P.wake_width) {
      let sdir = sdf.rg * 2.0 - vec2<f32>(1.0, 1.0);
      let ndir = vec2<f32>(sdir.x, -sdir.y); // NDC outward (screen +y down → NDC +y up)
      let ff = 1.0 - smoothstep(0.0, P.wake_width, dist);
      v += ndir * ff * P.push * (1.0 + P.wake) * 2.0 * P.dt; // steady radial berth
    }
    let muv = (suv0 + vec2<f32>(1.0, 1.0) - vec2<f32>(P.menu_du, P.menu_dv)) / 3.0;
    if (abs(muv.x - P.pad0) < 0.1667 && abs(muv.y - P.pad1) < 0.1667) {
      let msdf = textureSampleLevel(menusdf, fsamp, muv, 0.0);
      let mdist = msdf.b * 0.35;
      if (mdist < P.wake_width) {
        let msdir = msdf.rg * 2.0 - vec2<f32>(1.0, 1.0);
        let mndir = vec2<f32>(msdir.x, -msdir.y);
        let mff = 1.0 - smoothstep(0.0, P.wake_width, mdist);
        v += mndir * mff * P.push * (1.0 + P.wake) * 2.0 * P.dt;
      }
    }
  }
  // SECTION TITLE berth: when a section title is scaled up, part the particles
  // around its SDF (the same berth as the words). The particles/bg never scale —
  // only the sampling maps screen → title-uv, and the SDF distance scales with the
  // title (×scale) so the berth stays a screen-space band hugging the giant glyph.
  if (titlex.w > 0.02) {
    let s = max(titlex.x, 0.001);
    let suv0 = vec2<f32>(pt.pos.x * 0.5 + 0.5, 0.5 - pt.pos.y * 0.5);
    let tuv = vec2<f32>(titlex.y, titlex.z)
      + (suv0 - vec2<f32>(0.5, 0.5)) / s * vec2<f32>(1.0, P.res.y / P.res.x);
    if (tuv.x > 0.0 && tuv.x < 1.0 && tuv.y > 0.0 && tuv.y < 1.0) {
      let tsdf = textureSampleLevel(title_sdf, title_samp, tuv, 0.0);
      let tdist = tsdf.b * 0.1 * s; // title-uv distance (maxdist 0.1) → screen-space
      // RENORMALIZE: linear filtering blends the dir field to a sub-unit vector in
      // the medial channels between strokes (wide on screen at scale) → the push
      // would collapse there. aspect-correct the NDC outward like the snap does.
      let traw = tsdf.rg * 2.0 - vec2<f32>(1.0, 1.0);
      let tlen = length(traw);
      if (tdist < P.wake_width && tlen > 1e-3) {
        let aspect = P.res.x / P.res.y;
        let tn = traw / tlen;
        let tndir = vec2<f32>(tn.x, -tn.y * aspect);
        let tff = 1.0 - smoothstep(0.0, P.wake_width, tdist);
        v += tndir * tff * P.push * (1.0 + P.wake) * 2.0 * P.dt;
      }
    }
  }
  // never trap: inside the field particles may only SLOW, never stall — keep a
  // minimum drift so they always wash out of the letterforms
  if (f > 0.25) {
    let minsp = P.stream * 0.45;
    let sp2 = length(v);
    if (sp2 < minsp) {
      var dirv = mdir;
      if (sp2 > 1e-4) { dirv = v / sp2; }
      v = dirv * minsp;
    }
  }

  // mouse/touch PERTURBATION — the primary interaction now (boosted): a wider, softer
  // "finger through the water" that shoves the stream around the cursor.
  let md = pt.pos - P.mouse;
  let mr2 = dot(md, md);
  if (P.mousef > 0.001 && mr2 < 0.22) {
    v += (md / max(sqrt(mr2), 0.02)) * P.mousef * exp(-mr2 * 13.0) * P.dt;
  }

  // speed cap — keeps the flow bounded
  let sp = length(v);
  if (sp > 1.4) { v *= 1.4 / sp; }

  var pos = pt.pos + v * P.dt;

  // SNOW PLOW = position SNAP-TO-EDGE. A particle the MOVING word is bearing down on
  // (closing > 0) may not end the frame INSIDE the wake — it's projected to the
  // forward edge. Pure position constraint, so the word can't out-step it (no skim),
  // it never reaches beyond wake_width (no expansion): a faster word carries
  // particles at its edge; a slower one lets the flow separate them. dist is in
  // screen-width units, so work in isotropic screen space (suv.y/aspect) and back.
  // ANTI-TUNNEL: a fast word can step PAST a particle between frames without any
  // frame catching the overlap. So a 2nd probe samples the wake SDF one velocity-
  // mapped step back along the word's path (no tail at the start of a slow drag;
  // capped at line_h so it can't bridge a line-gap and snap to the wrong line).
  // That 2nd probe is a tunnel DETECTOR only — the displacement is ALWAYS driven by
  // probe A (sampled at the particle itself), so the particle lands exactly on the
  // current word's wake edge and is never pushed the wrong way. Engaged while
  // pressed OR still moving.
  if (engaged) {
    let aspect = P.res.x / P.res.y;
    let line_h = 0.06; // back-probe cap (< the smallest phone line gap)
    let scuv = vec2<f32>(pos.x * 0.5 + 0.5, 0.5 - pos.y * 0.5);
    // --- name + phrase ---
    let suvA = scuv - vec2<f32>(P.text_du, P.text_dv);
    let sa = textureSampleLevel(sdftex, fsamp, suvA, 0.0); // probe A: at the particle
    let nd = sa.b * 0.35;
    // RENORMALIZE: linear filtering across the direction field's discontinuities
    // returns a sub-unit vector; without this the snap under-displaces (residual skim).
    let nraw = sa.rg * 2.0 - vec2<f32>(1.0, 1.0);
    let nlen = length(nraw);
    let ns = select(vec2<f32>(0.0, 0.0), nraw / nlen, nlen > 1e-3); // isotropic-screen outward
    let nndc = vec2<f32>(ns.x, -ns.y * aspect); // NDC outward (aspect-correct)
    let nclos = dot(vec2<f32>(P.text_vx, P.text_vy), nndc);
    // probe B: tunnel detector — distance only, one step back along the path
    let trav = vec2<f32>(P.text_vx * 0.5, -P.text_vy * 0.5) * P.dt;
    let tl = length(trav);
    let noff = select(vec2<f32>(0.0, 0.0), trav / tl * min(tl, line_h), tl > 1e-6);
    let ndB = textureSampleLevel(sdftex, fsamp, suvA + noff, 0.0).b * 0.35;
    // fire when inside the wake on the leading side, OR (genuine tunnel) probe A
    // MISSED it but the detector caught it while moving. Displace from A's geometry.
    let hit = (nd < P.wake_width && nclos > 0.0) || (nd >= P.wake_width && ndB < P.wake_width && tl > 1e-5);
    if (nlen > 1e-3 && hit) {
      let isp = vec2<f32>(scuv.x, scuv.y / aspect) + ns * (P.wake_width - nd);
      let su = vec2<f32>(isp.x, isp.y * aspect);
      pos = vec2<f32>(su.x * 2.0 - 1.0, 1.0 - su.y * 2.0);
      let vn = normalize(nndc);
      let vin = dot(v, vn);
      if (vin < 0.0) { v -= vn * vin; } // drop inward velocity so it won't fight the edge
    }
    // --- active off-screen panel (same scheme, in the panned atlas frame /3) ---
    let scuv2 = vec2<f32>(pos.x * 0.5 + 0.5, 0.5 - pos.y * 0.5);
    let mu = (scuv2 + vec2<f32>(1.0, 1.0) - vec2<f32>(P.menu_du, P.menu_dv)) / 3.0;
    if (abs(mu.x - P.pad0) < 0.1667 && abs(mu.y - P.pad1) < 0.1667) {
      let ma = textureSampleLevel(menusdf, fsamp, mu, 0.0); // probe A
      let md2 = ma.b * 0.35;
      let mraw = ma.rg * 2.0 - vec2<f32>(1.0, 1.0);
      let mlen = length(mraw);
      let ms = select(vec2<f32>(0.0, 0.0), mraw / mlen, mlen > 1e-3);
      let mndc = vec2<f32>(ms.x, -ms.y * aspect);
      let mclos = dot(vec2<f32>(P.menu_vx, P.menu_vy), mndc);
      let mtrav = vec2<f32>(P.menu_vx * 0.5, -P.menu_vy * 0.5) * P.dt;
      let mtl = length(mtrav);
      let moff = select(vec2<f32>(0.0, 0.0), mtrav / mtl * min(mtl, line_h) / 3.0, mtl > 1e-6);
      let mdB = textureSampleLevel(menusdf, fsamp, mu + moff, 0.0).b * 0.35; // detector
      let mhit = (md2 < P.wake_width && mclos > 0.0) || (md2 >= P.wake_width && mdB < P.wake_width && mtl > 1e-5);
      if (mlen > 1e-3 && mhit) {
        let isp2 = vec2<f32>(scuv2.x, scuv2.y / aspect) + ms * (P.wake_width - md2);
        let su2 = vec2<f32>(isp2.x, isp2.y * aspect);
        pos = vec2<f32>(su2.x * 2.0 - 1.0, 1.0 - su2.y * 2.0);
        let vn2 = normalize(mndc);
        let vin2 = dot(v, vn2);
        if (vin2 < 0.0) { v -= vn2 * vin2; }
      }
    }
  }

  // respawn: recycle only particles that exited DOWNSTREAM (or wandered far),
  // and re-enter them on a spawn line beyond the viewport's corner radius
  // (sqrt(2)≈1.41) so the origin edge is never visible at any heading/aspect
  let fdir = vec2<f32>(cos(th), sin(th));
  let outside = abs(pos.x) > 1.02 || abs(pos.y) > 1.02;
  if ((outside && dot(pos, fdir) > 1.05) || length(pos) > 2.6) {
    let perp = vec2<f32>(-fdir.y, fdir.x);
    let eta = (rand01(i + u32(P.time * 16.0) * 2659u) * 2.0 - 1.0) * 1.65;
    pos = -fdir * 1.55 + perp * eta;
    v = fdir * P.stream;
  }

  pt.pos = pos;
  pt.vel = v;
  parts[i] = pt;
}
"#;

// Scene pass: bright normal-map background + sharp white text (field channel
// G) + instanced additive light particles. Renders into the HDR buffer.
const DRAW_SHADER: &str = r#"
struct Params {
  res: vec2<f32>, mouse: vec2<f32>,
  time: f32, dt: f32, count: u32, stream: f32,
  push: f32, mousef: f32, dpr: f32, rot_speed: f32,
  rot_depth: f32, turb: f32, eddy: f32, sparkg: f32,
  bg_freq: f32, text_sat: f32, bg_speed: f32, mobile: f32,
  phrase_w: f32, phrase_op: f32, phrase_z: f32, phrase_cy: f32,
  bg_fade: f32, part_fade: f32, name_op: f32, intro_glow: f32,
  text_du: f32, text_dv: f32, text_vx: f32, text_vy: f32,
  menu_du: f32, menu_dv: f32, pad0: f32, pad1: f32,
  wake: f32, porosity: f32, pressed: f32, wake_width: f32,
  press_z: f32, menu_vx: f32, menu_vy: f32, bump: f32,
};
@group(0) @binding(0) var<uniform> P: Params;
@group(0) @binding(1) var field: texture_2d<f32>;
@group(0) @binding(2) var fsamp: sampler;

// ---------- bright screen-space normal map + sharp white type ----------
@vertex
fn vs_bg(@builtin(vertex_index) i: u32) -> @builtin(position) vec4<f32> {
  var p = array<vec2<f32>, 3>(vec2<f32>(-1.,-1.), vec2<f32>(3.,-1.), vec2<f32>(-1.,3.));
  return vec4<f32>(p[i], 0., 1.);
}
fn bedHeight(p: vec2<f32>, t: f32) -> f32 {
  return sin(p.x * 3.0 + t) * 0.55 + cos(p.y * 3.6 - t * 0.8) * 0.55
       + sin(p.x * 4.6 + p.y * 1.9 + t * 0.55) * 0.30
       + sin(p.x * 5.1 - t * 0.70) * cos(p.y * 4.3 + t * 0.45) * 0.12
       + sin((p.x * 1.7 - p.y * 2.3) * 2.6 - t * 0.9) * 0.10;
}
@fragment
fn fs_bg(@builtin(position) frag: vec4<f32>) -> @location(0) vec4<f32> {
  let uv = frag.xy / P.res;
  let aspect = P.res.x / P.res.y;
  let t = P.time * 0.35 * P.bg_speed;
  // the pattern slowly ROTATES on a simple noise walk (two incommensurate
  // sines — bounded, non-repeating), plus a visible drift; both ride bg_speed
  let ba = 0.8 * sin(t * 0.17) + 0.5 * sin(t * 0.063 + 1.3);
  let ca = cos(ba);
  let sa = sin(ba);
  // CAMERA: the bg is one continuous world — pan it with the swipe so it EXTENDS into
  // the revealed off-screen areas. Sample the infinite sine field at the camera offset,
  // matching the text/shadow's -text_du/dv (line below) so the whole world moves as one.
  let buv = uv - vec2<f32>(P.text_du, P.text_dv);
  let p0 = vec2<f32>((buv.x - 0.5) * aspect, buv.y - 0.5) * 3.0 * P.bg_freq;
  let p = vec2<f32>(p0.x * ca - p0.y * sa, p0.x * sa + p0.y * ca)
        + vec2<f32>(t * 0.55, -t * 0.34);

  // screen-space normal from a layered height field
  let e = 0.018;
  let hC = bedHeight(p, t);
  let dx = hC - bedHeight(p + vec2<f32>(e, 0.0), t);
  let dy = hC - bedHeight(p + vec2<f32>(0.0, e), t);
  let n = normalize(vec3<f32>(dx * 7.0, dy * 7.0, 1.0));
  let l = normalize(vec3<f32>(cos(t * 0.55) * 0.75, sin(t * 0.55) * 0.75, 0.62));
  let vv = vec3<f32>(0.0, 0.0, 1.0);
  let diff = max(dot(n, l), 0.0);
  let spec = pow(max(dot(reflect(-l, n), vv), 0.0), 22.0);

  // BRIGHT normal-map palette, blues/violets suppressed: the normal's RG
  // encode picks the hue (salmon → lime), diffuse+specular light it hot
  let enc = n.xy * 0.5 + vec2<f32>(0.5, 0.5);
  var col = vec3<f32>(
    0.34 + 0.62 * enc.x,
    0.30 + 0.58 * enc.y,
    0.22 + 0.14 * (1.0 - enc.x)
  );
  col *= 0.34 + 0.78 * diff;
  col += vec3<f32>(1.0, 0.92, 0.74) * spec * 0.35;

  // soft drop shadow from the blurred field (R). The type itself is NOT in
  // the scene — it's composited ABOVE the bloom in the final pass, so glow
  // can never overlap the letter edges. Recede it with the text (press_z) so a
  // press doesn't leave the shadow as an outline at the original z.
  let zuv = vec2<f32>(0.5, 0.5) + (uv - vec2<f32>(0.5, 0.5)) / max(P.press_z, 0.01);
  let fr = textureSampleLevel(field, fsamp, zuv - vec2<f32>(P.text_du, P.text_dv), 0.0);
  let fc = textureSampleLevel(field, fsamp, zuv, 0.0);
  let name_sh = fr.r * (1.0 - fr.g) * P.name_op;
  let phrase_sh = fc.b * P.phrase_w * (1.0 - fc.a);
  let shadow = max(name_sh, phrase_sh);
  col *= 1.0 - 0.55 * shadow;

  return vec4<f32>(col * P.bg_fade, 1.0);
}

// ---------- particles: instanced soft quads, additive light ----------
struct VOut {
  @builtin(position) pos: vec4<f32>,
  @location(0) col: vec3<f32>,
  @location(1) quv: vec2<f32>,
};

fn pcg(v: u32) -> u32 {
  var s = v * 747796405u + 2891336453u;
  s = ((s >> ((s >> 28u) + 4u)) ^ s) * 277803737u;
  return (s >> 22u) ^ s;
}
fn rand01(v: u32) -> f32 { return f32(pcg(v)) / 4294967295.0; }

@vertex
fn vs_p(
  @builtin(vertex_index) vi: u32,
  @builtin(instance_index) ii: u32,
  @location(0) ppos: vec2<f32>,
  @location(1) pvel: vec2<f32>,
) -> VOut {
  var corners = array<vec2<f32>, 6>(
    vec2<f32>(-1.,-1.), vec2<f32>(1.,-1.), vec2<f32>(1.,1.),
    vec2<f32>(-1.,-1.), vec2<f32>(1.,1.),  vec2<f32>(-1.,1.)
  );
  let speed = length(pvel);
  let dir = pvel / max(speed, 1e-5);

  // ── color: a portion of a normal map, violet/blue deprioritized ──
  // vertical deflection exaggerated so parting flow shifts lime/crimson
  let edir = normalize(vec2<f32>(dir.x, dir.y * 2.2));
  let enc = edir * 0.5 + vec2<f32>(0.5, 0.5);
  var col = vec3<f32>(
    0.20 + 0.80 * enc.x,
    0.18 + 0.82 * enc.y,
    0.15 + 0.18 * (1.0 - enc.x)
  );

  // brightness follows speed (fast water is bright)
  let relsp = clamp(speed / max(P.stream, 0.01), 0.0, 1.6);
  var lum = 0.50 + 0.55 * relsp;

  // ── noon sparkle: stalled particles glint HOT (HDR — the bloom feeds on it)
  let h1 = rand01(ii);
  let h2 = rand01(ii ^ 0x68bc21ebu);
  let tw = pow(max(sin(P.time * (2.0 + h1 * 7.0) + h2 * 6.2832), 0.0), 26.0);
  let stag = 1.0 - clamp(speed / max(P.stream, 0.01), 0.0, 1.0);
  let spark = min(tw * (0.05 + 2.4 * stag * stag) * P.sparkg, 1.4)
            * (1.0 - P.mobile * 0.5) * P.intro_glow;
  col = mix(col, vec3<f32>(1.45, 1.22, 0.78), clamp(spark, 0.0, 0.9));
  lum += spark * 3.2;

  let px = vec2<f32>(2.0, 2.0) / P.res;
  let size = (1.7 + h2 * 1.1 + spark * 7.0) * max(P.dpr, 1.0)
           * (1.0 - P.mobile * 0.3);
  // motion-stretch: fast particles smear into silky streamlines along their
  // velocity; stalled (sparkling) ones stay round — water silk vs. sun glints
  let stretch = size + min(speed * 26.0, 11.0) * max(P.dpr, 1.0) * (1.0 - clamp(spark, 0.0, 1.0));
  let along = dir * stretch;
  let perp = vec2<f32>(-dir.y, dir.x) * size;
  let off = (corners[vi].x * along + corners[vi].y * perp) * px;
  var o: VOut;
  o.pos = vec4<f32>(ppos + off, 0.0, 1.0);
  // intro: reveal the field top-to-bottom (particles above the descending
  // line are lit, soft leading edge); rests fully revealed after the intro
  o.col = col * lum * 0.10 * P.part_fade;
  o.quv = corners[vi];
  return o;
}

@fragment
fn fs_p(in: VOut) -> @location(0) vec4<f32> {
  let d = length(in.quv);
  var a = smoothstep(1.0, 0.0, d);
  a = a * a;
  return vec4<f32>(in.col * a, 1.0);
}
"#;

// Post chain: bright-extract + horizontal blur (half res) → vertical blur →
// composite (scene + bloom, filmic tonemap) to the swapchain.
const POST_SHADER: &str = r#"
struct VOut { @builtin(position) pos: vec4<f32>, @location(0) uv: vec2<f32> };

@vertex
fn vs_full(@builtin(vertex_index) i: u32) -> VOut {
  var p = array<vec2<f32>, 3>(vec2<f32>(-1.,-1.), vec2<f32>(3.,-1.), vec2<f32>(-1.,3.));
  var o: VOut;
  o.pos = vec4<f32>(p[i], 0., 1.);
  o.uv = vec2<f32>(p[i].x * 0.5 + 0.5, 0.5 - p[i].y * 0.5);
  return o;
}

@group(0) @binding(0) var src: texture_2d<f32>;
@group(0) @binding(1) var samp: sampler;
@group(0) @binding(3) var fieldtex: texture_2d<f32>;

const W0: f32 = 0.227027;
const W1: f32 = 0.194595;
const W2: f32 = 0.121622;
const W3: f32 = 0.054054;
const W4: f32 = 0.016216;

fn bright(uv: vec2<f32>) -> vec3<f32> {
  let c = textureSampleLevel(src, samp, uv, 0.0).rgb;
  // soft-knee bright pass: keep what exceeds the threshold. The text isn't in
  // the scene at all, so the bloom needs no masking anywhere.
  let lum = dot(c, vec3<f32>(0.2126, 0.7152, 0.0722));
  let k = smoothstep(0.78, 1.15, lum);
  return c * k;
}

@fragment
fn fs_bright_h(in: VOut) -> @location(0) vec4<f32> {
  let texel = 1.0 / vec2<f32>(textureDimensions(src));
  var acc = bright(in.uv) * W0;
  acc += (bright(in.uv + vec2<f32>(texel.x * 1.0, 0.0)) + bright(in.uv - vec2<f32>(texel.x * 1.0, 0.0))) * W1;
  acc += (bright(in.uv + vec2<f32>(texel.x * 2.0, 0.0)) + bright(in.uv - vec2<f32>(texel.x * 2.0, 0.0))) * W2;
  acc += (bright(in.uv + vec2<f32>(texel.x * 3.0, 0.0)) + bright(in.uv - vec2<f32>(texel.x * 3.0, 0.0))) * W3;
  acc += (bright(in.uv + vec2<f32>(texel.x * 4.0, 0.0)) + bright(in.uv - vec2<f32>(texel.x * 4.0, 0.0))) * W4;
  return vec4<f32>(acc, 1.0);
}

@fragment
fn fs_blur_v(in: VOut) -> @location(0) vec4<f32> {
  let texel = 1.0 / vec2<f32>(textureDimensions(src));
  var acc = textureSampleLevel(src, samp, in.uv, 0.0).rgb * W0;
  acc += (textureSampleLevel(src, samp, in.uv + vec2<f32>(0.0, texel.y * 1.0), 0.0).rgb
        + textureSampleLevel(src, samp, in.uv - vec2<f32>(0.0, texel.y * 1.0), 0.0).rgb) * W1;
  acc += (textureSampleLevel(src, samp, in.uv + vec2<f32>(0.0, texel.y * 2.0), 0.0).rgb
        + textureSampleLevel(src, samp, in.uv - vec2<f32>(0.0, texel.y * 2.0), 0.0).rgb) * W2;
  acc += (textureSampleLevel(src, samp, in.uv + vec2<f32>(0.0, texel.y * 3.0), 0.0).rgb
        + textureSampleLevel(src, samp, in.uv - vec2<f32>(0.0, texel.y * 3.0), 0.0).rgb) * W3;
  acc += (textureSampleLevel(src, samp, in.uv + vec2<f32>(0.0, texel.y * 4.0), 0.0).rgb
        + textureSampleLevel(src, samp, in.uv - vec2<f32>(0.0, texel.y * 4.0), 0.0).rgb) * W4;
  return vec4<f32>(acc, 1.0);
}

@group(0) @binding(2) var bloom: texture_2d<f32>;
struct Params {
  res: vec2<f32>, mouse: vec2<f32>,
  time: f32, dt: f32, count: u32, stream: f32,
  push: f32, mousef: f32, dpr: f32, rot_speed: f32,
  rot_depth: f32, turb: f32, eddy: f32, sparkg: f32,
  bg_freq: f32, text_sat: f32, bg_speed: f32, mobile: f32,
  phrase_w: f32, phrase_op: f32, phrase_z: f32, phrase_cy: f32,
  bg_fade: f32, part_fade: f32, name_op: f32, intro_glow: f32,
  text_du: f32, text_dv: f32, text_vx: f32, text_vy: f32,
  menu_du: f32, menu_dv: f32, pad0: f32, pad1: f32,
  wake: f32, porosity: f32, pressed: f32, wake_width: f32,
  press_z: f32, menu_vx: f32, menu_vy: f32, bump: f32,
};
@group(0) @binding(4) var<uniform> P: Params;
@group(0) @binding(5) var menutex: texture_2d<f32>;
// section-title transform (own bind group): x=scale, yz=offset(title uv), w=on
@group(1) @binding(0) var<uniform> titlex: vec4<f32>;
@group(1) @binding(1) var title_sdf: texture_2d<f32>;
@group(1) @binding(2) var title_samp: sampler;

// the TEXT's own relief — sampled in continuous screen space so one surface
// spans the whole text block; rendered here, ABOVE scene + bloom
fn textHeight(p: vec2<f32>, t: f32) -> f32 {
  return sin(p.x * 2.2 - t * 0.9) * 0.60
       + cos(p.y * 2.8 + t * 0.7) * 0.60
       + sin((p.x - p.y) * 4.6 + t * 1.1) * 0.35
       + cos(p.x * 7.0 + p.y * 5.0 - t * 0.5) * 0.18;
}

// warm normal-mapped relief, shaded in screen space, for any text pixel
fn reliefCol(suv: vec2<f32>) -> vec3<f32> {
  let aspect = P.res.x / P.res.y;
  let tt = P.time * 0.45;
  let tp = vec2<f32>((suv.x - 0.5) * aspect, suv.y - 0.5) * 2.4
         + vec2<f32>(tt * 0.35, -tt * 0.22);
  let te = 0.02;
  let thC = textHeight(tp, tt);
  let tdx = thC - textHeight(tp + vec2<f32>(te, 0.0), tt);
  let tdy = thC - textHeight(tp + vec2<f32>(0.0, te), tt);
  let n2 = normalize(vec3<f32>(tdx * 5.0, tdy * 5.0, 1.0));
  let tl = P.time * 0.19;
  let l2 = normalize(vec3<f32>(cos(tl + 2.4) * 0.75, sin(tl + 2.4) * 0.75, 0.62));
  let vv = vec3<f32>(0.0, 0.0, 1.0);
  let d2 = max(dot(n2, l2), 0.0);
  let s2 = pow(max(dot(reflect(-l2, n2), vv), 0.0), 30.0);
  let enc2 = clamp(n2.xy * 2.2, vec2<f32>(-1.0, -1.0), vec2<f32>(1.0, 1.0)) * 0.5
           + vec2<f32>(0.5, 0.5);
  let hue = vec3<f32>(
    0.25 + 0.75 * enc2.x,
    0.30 + 0.70 * enc2.y,
    0.06 + 0.10 * (1.0 - enc2.y)
  );
  var tcol = mix(vec3<f32>(1.0, 1.0, 1.0), hue, P.text_sat);
  let tlum = dot(tcol, vec3<f32>(0.2126, 0.7152, 0.0722));
  tcol = tcol * mix(1.0, 1.02 / max(tlum, 0.30), 0.7);
  tcol = tcol * (0.92 + 0.55 * d2) + vec3<f32>(1.15, 1.00, 0.78) * s2 * 0.90;
  return tcol * 1.18;
}

fn aces(x: vec3<f32>) -> vec3<f32> {
  return clamp((x * (2.51 * x + 0.03)) / (x * (2.43 * x + 0.59) + 0.14),
               vec3<f32>(0.0), vec3<f32>(1.0));
}

@fragment
fn fs_comp(in: VOut) -> @location(0) vec4<f32> {
  let scene = textureSampleLevel(src, samp, in.uv, 0.0).rgb;
  let glow = textureSampleLevel(bloom, samp, in.uv, 0.0).rgb;
  // full, untouched bloom everywhere — the text is drawn ON TOP, occluding it
  var c = aces((scene + glow * 1.25 * P.intro_glow) * 0.92);

  // press recede: scale the TEXT sampling around screen centre (press_z<1 pushes
  // the visible words back to meet the wake). The scene/bloom keep in.uv.
  let zuv = vec2<f32>(0.5, 0.5) + (in.uv - vec2<f32>(0.5, 0.5)) / max(P.press_z, 0.01);

  // NAME (G) — permanent, screen-locked, full opacity
  // NAME (G) — resolves soft->crisp and fades in during the intro, then locked
  let name_c = smoothstep(0.42, 0.55, textureSampleLevel(fieldtex, samp, zuv - vec2<f32>(P.text_du, P.text_dv), 0.0).g) * P.name_op;
  // PHRASE (A) — fades + pushes in z: scale the MASK sampling around the
  // phrase center (z<1 expands the sample → glyphs shrink → pushed back),
  // and multiply coverage by opacity. A fading phrase reveals the bloom again.
  let pivot = vec2<f32>(0.5, P.phrase_cy);
  let puv = pivot + (zuv - pivot) / max(P.phrase_z, 0.01);
  let phrase_c = smoothstep(0.42, 0.55,
    textureSampleLevel(fieldtex, samp, puv, 0.0).a) * P.phrase_op;

  let m_uv = (zuv + vec2<f32>(1.0, 1.0) - vec2<f32>(P.menu_du, P.menu_dv)) / 3.0;
  let m_in = abs(m_uv.x - P.pad0) < 0.1667 && abs(m_uv.y - P.pad1) < 0.1667;
  let menu_c = select(0.0, smoothstep(0.42, 0.55, textureSampleLevel(menutex, samp, m_uv, 0.0).g), m_in);
  // SECTION TITLE — scaled up (not a scene zoom): map the screen into title-uv
  // around the path offset, sample the title SDF, threshold to a crisp letterform.
  // The particles/bg keep in.uv (untouched); only the title is transformed.
  var title_c = 0.0;
  if (titlex.w > 0.02) {
    let s = max(titlex.x, 0.001);
    let tuv = vec2<f32>(titlex.y, titlex.z)
      + (in.uv - vec2<f32>(0.5, 0.5)) / s * vec2<f32>(1.0, P.res.y / P.res.x);
    if (tuv.x > 0.0 && tuv.x < 1.0 && tuv.y > 0.0 && tuv.y < 1.0) {
      let td = textureSampleLevel(title_sdf, title_samp, tuv, 0.0).b * 0.1; // exterior dist (maxdist 0.1)
      // AA band: ~1 screen px, but floored to ~1 SDF texel (2048²) so the threshold
      // spans a texel and smooths the pixel-grid steps without over-blurring.
      let aa = max((1.0 / P.res.x) / s, 0.0006);
      title_c = 1.0 - smoothstep(0.0, aa, td);
    }
  }
  // the home name/phrase slide fully OVER via tx_off (sampled at zuv - text_du/dv),
  // so they leave on their own — no fade. Only the small east panel is replaced by
  // the big title, so suppress just that as the title comes in.
  let menu_fade = 1.0 - smoothstep(0.0, 0.3, titlex.w);
  let cover = max(max(max(name_c, phrase_c), menu_c * menu_fade), title_c);
  if (cover > 0.001) {
    c = mix(c, aces(reliefCol(in.uv) * 0.92), cover);
  }
  return vec4<f32>(c, 1.0);
}
"#;

fn rnd(s: &mut u32) -> f32 {
    *s ^= *s << 13;
    *s ^= *s >> 17;
    *s ^= *s << 5;
    (*s as f32) / (u32::MAX as f32)
}

// read a live tuning value from window.__DIALS (set by the debug panel)
fn dial(name: &str, default: f32) -> f32 {
    web_sys::window()
        .map(JsValue::from)
        .and_then(|w| js_sys::Reflect::get(&w, &"__DIALS".into()).ok())
        .and_then(|o| js_sys::Reflect::get(&o, &name.into()).ok())
        .and_then(|v| v.as_f64())
        .map(|v| v as f32)
        .unwrap_or(default)
}

// one phrase from window.__PHRASES: a plain string (the phrases.json format), or an
// object with an `action_phrase` field (legacy sections.json compat). trimmed/non-empty.
fn phrase_entry(v: &wasm_bindgen::JsValue) -> Option<String> {
    let s = v.as_string().or_else(|| {
        js_sys::Reflect::get(v, &"action_phrase".into())
            .ok()
            .and_then(|x| x.as_string())
    })?;
    let s = s.trim().to_string();
    if s.is_empty() { None } else { Some(s) }
}

// the live phrase list from window.__PHRASES (editor panel); falls back to the baked
// phrases.json when unset or empty so the viz never breaks.
fn current_action_phrases(fallback: &[String]) -> Vec<String> {
    let arr = web_sys::window()
        .and_then(|w| js_sys::Reflect::get(&w, &"__PHRASES".into()).ok())
        .and_then(|v| v.dyn_into::<js_sys::Array>().ok());
    if let Some(a) = arr {
        let mut out = Vec::new();
        for i in 0..a.length() {
            if let Some(p) = phrase_entry(&a.get(i)) { out.push(p); }
        }
        if !out.is_empty() { return out; }
    }
    fallback.to_vec()
}

// window.__PHRASES_GEN: content.js bumps it when it swaps the phrase list (a
// route change); the cycle then lets the current words go at once and comes
// back with the new list's first phrase, instead of waiting out its hold.
fn phrases_gen() -> f64 {
    web_sys::window()
        .and_then(|w| js_sys::Reflect::get(&w, &"__PHRASES_GEN".into()).ok())
        .and_then(|v| v.as_f64())
        .unwrap_or(0.0)
}

// ── host API: window.site, defined by /site-host.js when this runs as a front
// end inside the site's sandboxed iframe (docs/frontend-protocol.md). Absent
// under `trunk serve`, so every call is a silent no-op there.
fn site_call(method: &str, args: &[JsValue]) {
    let Some(w) = web_sys::window() else { return };
    let Ok(site) = js_sys::Reflect::get(&w, &"site".into()) else { return };
    if !site.is_object() {
        return;
    }
    if let Ok(f) = js_sys::Reflect::get(&site, &method.into()) {
        if let Some(f) = f.dyn_ref::<js_sys::Function>() {
            let args: js_sys::Array = args.iter().collect();
            let _ = f.apply(&site, &args);
        }
    }
}

// report a fatal problem: console + site.reportError(err, kind). Before
// site.ready() any report makes the parent fall back; "gpu-lost" always does.
fn site_error(msg: &str, kind: &str) {
    web_sys::console::error_1(&msg.into());
    site_call("reportError", &[js_sys::Error::new(msg).into(), kind.into()]);
}

// top-level page (trunk serve / direct load) vs. embedded in the site's iframe
fn standalone() -> bool {
    let Some(w) = web_sys::window() else { return true };
    let w: JsValue = w.into();
    js_sys::Reflect::get(&w, &"top".into())
        .map(|t| js_sys::Object::is(&t, &w))
        .unwrap_or(false)
}

fn set_status(text: &str) {
    if let Some(el) = web_sys::window()
        .and_then(|w| w.document())
        .and_then(|d| d.get_element_by_id("fps"))
    {
        el.set_text_content(Some(text));
    }
}

// tier + capped font sizes, shared by name/phrase layout so they agree
fn tier_fonts(w: u32, h: u32, css_w: f64) -> (bool, bool, f64, f64) {
    let (wf, hf) = (w as f64, h as f64);
    let phone = hf > wf * 1.6; // aspect < ~0.62
    let portrait = hf > wf * 0.85;
    let f1 = (wf * 0.118).min(wf * 118.0 / css_w.max(1.0));
    let f2 = (wf * 0.064).min(wf * 62.0 / css_w.max(1.0));
    (phone, portrait, f1, f2)
}

// NAME removed (see restructure): the cycling phrase is the only text now. Kept as an
// empty layout so the field's R/G "name" channels stay blank everywhere it's rastered.
fn name_layout(_w: u32, _h: u32, _css_w: f64) -> Vec<(String, f64, f64)> {
    Vec::new()
}

// the PHRASE is centered on the screen middle (the name is gone). Wraps by MEASURED
// pixel width (canvas measureText, not a char-count guess) so no line ever overflows
// the field width, then shrinks the whole block to fit both width AND height. Returns
// its lines + block center (uv.y = 0.5), the composite's z-scale pivot.
fn phrase_layout(
    ctx: &web_sys::CanvasRenderingContext2d,
    w: u32,
    h: u32,
    css_w: f64,
    phrase: &str,
) -> (Vec<(String, f64, f64)>, f64) {
    let (wf, hf) = (w as f64, h as f64);
    let (phone, portrait, _f1, f2) = tier_fonts(w, h, css_w);
    let base = if phone {
        wf * 0.138 // mobile: enlarged type
    } else if portrait {
        wf * 0.088
    } else {
        f2
    };
    let max_w = wf * 0.9; // every line stays inside the field width (small margin)
    // measure at the unscaled base font — same family/weight raster_layer draws with
    ctx.set_font(&format!("900 {:.0}px -apple-system, system-ui, sans-serif", base));
    let measure = |s: &str| ctx.measure_text(s).map(|m| m.width()).unwrap_or(0.0);
    // greedy wrap by MEASURED WIDTH: keep adding words while the line still fits max_w,
    // wrapping the instant it wouldn't — so a wide line can't spill off the edges.
    let mut lines: Vec<String> = Vec::new();
    let mut cur = String::new();
    for wd in phrase.split(' ') {
        if cur.is_empty() {
            cur = wd.to_string();
        } else {
            let cand = format!("{} {}", cur, wd);
            if measure(&cand) <= max_w {
                cur = cand;
            } else {
                lines.push(std::mem::take(&mut cur));
                cur = wd.to_string();
            }
        }
    }
    if !cur.is_empty() {
        lines.push(cur);
    }
    if lines.is_empty() {
        lines.push(phrase.to_string());
    }
    // shrink-to-fit backstop: an unbreakable single word wider than max_w scales down to
    // fit the width; the whole stack scales down if it exceeds ~78% of the height.
    let widest = lines.iter().map(|l| measure(l)).fold(0.0_f64, f64::max);
    let n = lines.len() as f64;
    let mut fs = base;
    let mut gap = fs * 1.28;
    let w_scale = if widest > max_w { max_w / widest } else { 1.0 };
    let block_h = gap * (n - 1.0) + fs;
    let h_scale = if block_h > hf * 0.78 { hf * 0.78 / block_h } else { 1.0 };
    let s = w_scale.min(h_scale);
    fs *= s;
    gap *= s;
    let mut e = Vec::new();
    let top = hf * 0.5 - gap * (n - 1.0) * 0.5; // center the block on the middle
    let mut y = top;
    for ln in &lines {
        e.push((ln.clone(), fs, y));
        y += gap;
    }
    (e, 0.5)
}

// rasterize a set of text entries into a (blur, sharp) coverage pair (R channel)
fn raster_layer(
    ctx: &web_sys::CanvasRenderingContext2d,
    w: u32,
    h: u32,
    entries: &[(String, f64, f64)],
) -> (Vec<u8>, Vec<u8>) {
    let (wf, hf) = (w as f64, h as f64);
    let draw = |ctx: &web_sys::CanvasRenderingContext2d| {
        for (text, px, y) in entries {
            ctx.set_font(&format!("900 {:.0}px -apple-system, system-ui, sans-serif", px));
            ctx.fill_text(text, wf / 2.0, *y).ok();
        }
    };
    let clear = |ctx: &web_sys::CanvasRenderingContext2d| {
        ctx.set_filter("none");
        ctx.set_fill_style_str("#000000");
        ctx.fill_rect(0.0, 0.0, wf, hf);
        ctx.set_fill_style_str("#ffffff");
        ctx.set_text_align("center");
        ctx.set_text_baseline("middle");
    };
    clear(ctx);
    ctx.set_filter("blur(6px)");
    draw(ctx);
    ctx.set_filter("blur(2px)");
    draw(ctx);
    ctx.set_filter("none");
    let blur = ctx.get_image_data(0.0, 0.0, wf, hf).unwrap().data();
    clear(ctx);
    draw(ctx);
    let sharp = ctx.get_image_data(0.0, 0.0, wf, hf).unwrap().data();
    let n = (w * h) as usize;
    let mut b = Vec::with_capacity(n);
    let mut sh = Vec::with_capacity(n);
    for i in 0..n {
        b.push(blur[i * 4]);
        sh.push(sharp[i * 4]);
    }
    (b, sh)
}

// 8SSEDT exterior distance transform: for every pixel, the (dx,dy) offset to the
// nearest "inside" pixel (mask>127). Two sweeps; distance = |offset|. Canvas
// pixels are square in screen space (the field shares the screen aspect), so the
// offset is already screen-isotropic.
fn edt8(mask: &[u8], w: usize, h: usize) -> Vec<(i32, i32)> {
    const INF: i32 = 1 << 14;
    let mut g = vec![(INF, INF); w * h];
    for i in 0..w * h {
        if mask[i] > 127 {
            g[i] = (0, 0);
        }
    }
    let d2 = |p: (i32, i32)| p.0 * p.0 + p.1 * p.1;
    for y in 0..h {
        for x in 0..w {
            let mut c = g[y * w + x];
            if x > 0 { let n = g[y * w + x - 1]; let cc = (n.0 - 1, n.1); if d2(cc) < d2(c) { c = cc; } }
            if y > 0 { let n = g[(y - 1) * w + x]; let cc = (n.0, n.1 - 1); if d2(cc) < d2(c) { c = cc; } }
            if x > 0 && y > 0 { let n = g[(y - 1) * w + x - 1]; let cc = (n.0 - 1, n.1 - 1); if d2(cc) < d2(c) { c = cc; } }
            if x + 1 < w && y > 0 { let n = g[(y - 1) * w + x + 1]; let cc = (n.0 + 1, n.1 - 1); if d2(cc) < d2(c) { c = cc; } }
            g[y * w + x] = c;
        }
    }
    for y in (0..h).rev() {
        for x in (0..w).rev() {
            let mut c = g[y * w + x];
            if x + 1 < w { let n = g[y * w + x + 1]; let cc = (n.0 + 1, n.1); if d2(cc) < d2(c) { c = cc; } }
            if y + 1 < h { let n = g[(y + 1) * w + x]; let cc = (n.0, n.1 + 1); if d2(cc) < d2(c) { c = cc; } }
            if x + 1 < w && y + 1 < h { let n = g[(y + 1) * w + x + 1]; let cc = (n.0 + 1, n.1 + 1); if d2(cc) < d2(c) { c = cc; } }
            if x > 0 && y + 1 < h { let n = g[(y + 1) * w + x - 1]; let cc = (n.0 - 1, n.1 + 1); if d2(cc) < d2(c) { c = cc; } }
            g[y * w + x] = c;
        }
    }
    g
}

// turn a coverage mask into a wake SDF: RG = screen-space OUTWARD unit direction
// (away from the words), B = distance to the words as a fraction of a screen
// width (clamped to `maxdist`). `px_per_screen` = canvas px spanning one screen
// width (= w for a screen-sized canvas, = w/3 for the 3x3 menu atlas).
fn coverage_to_sdf(sharp: &[u8], w: u32, h: u32, px_per_screen: f32, maxdist: f32) -> Vec<u8> {
    let g = edt8(sharp, w as usize, h as usize);
    let n = (w * h) as usize;
    let mut out = vec![0u8; n * 4];
    let inv = 1.0 / maxdist;
    for i in 0..n {
        let (gx, gy) = g[i];
        let l = ((gx * gx + gy * gy) as f32).sqrt();
        let (dx, dy) = if l > 0.5 { (-(gx as f32) / l, -(gy as f32) / l) } else { (0.0, 0.0) };
        let d = (l / px_per_screen * inv).min(1.0); // 0 at the words → 1 at maxdist
        out[i * 4] = ((dx * 0.5 + 0.5) * 255.0).round().clamp(0.0, 255.0) as u8;
        out[i * 4 + 1] = ((dy * 0.5 + 0.5) * 255.0).round().clamp(0.0, 255.0) as u8;
        out[i * 4 + 2] = (d * 255.0).round().clamp(0.0, 255.0) as u8;
        out[i * 4 + 3] = 255;
    }
    out
}

// (the parked section-title and menu-atlas bakes live on in experiments/particle-stream)

// wake SDF for the on-screen words (name + phrase), sampled in their moving frame
fn bake_sdf(
    ctx: &web_sys::CanvasRenderingContext2d,
    sw: u32,
    sh: u32,
    css_w: f64,
    phrase: &str,
    maxdist: f32,
) -> Vec<u8> {
    let mut entries = name_layout(sw, sh, css_w);
    entries.extend(phrase_layout(ctx, sw, sh, css_w, phrase).0);
    let (_, sharp) = raster_layer(ctx, sw, sh, &entries);
    coverage_to_sdf(&sharp, sw, sh, sw as f32, maxdist)
}

fn pack_rgba(nb: &[u8], ns: &[u8], pb: &[u8], ps: &[u8]) -> Vec<u8> {
    let n = nb.len();
    let mut out = Vec::with_capacity(n * 4);
    for i in 0..n {
        out.push(nb[i]);
        out.push(ns[i]);
        out.push(pb[i]);
        out.push(ps[i]);
    }
    out
}

// ── the site scene (content.js): window.__SCENE describes the current page
// as obstacles, measured from the DOM in CSS px:
//   page   the current screen; slides through the stream (RG channels)
//   chrome the nav + pager; stays put (BA channels)
// each with glyphs {t, f, x, y} (big type the GPU draws: sharp + blur),
// words {t, f, x, y} (small type the DOM draws: a halo only) and boxes
// {x, y, w, h, r} (panels and media: solid). y is a text baseline.
#[derive(Default)]
struct SceneText {
    t: String,
    f: String,
    x: f64,
    y: f64,
}
#[derive(Default)]
struct SceneBox {
    x: f64,
    y: f64,
    w: f64,
    h: f64,
    r: f64,
}
#[derive(Default)]
struct ScenePart {
    glyphs: Vec<SceneText>,
    words: Vec<SceneText>,
    boxes: Vec<SceneBox>,
}
struct Scene {
    gen: f64,
    vw: f64,
    vh: f64,
    page: ScenePart,
    chrome: ScenePart,
}

// a performance.mark, for measuring time to first frame on real devices
fn mark(name: &str) {
    if let Some(p) = web_sys::window().and_then(|w| w.performance()) {
        let _ = js_sys::Reflect::get(&p, &"mark".into())
            .ok()
            .and_then(|f| f.dyn_into::<js_sys::Function>().ok())
            .map(|f| f.call1(&p, &name.into()));
    }
}

fn win_get(name: &str) -> JsValue {
    web_sys::window()
        .and_then(|w| js_sys::Reflect::get(&w, &name.into()).ok())
        .unwrap_or(JsValue::UNDEFINED)
}
fn js_f(o: &JsValue, k: &str) -> f64 {
    js_sys::Reflect::get(o, &k.into()).ok().and_then(|v| v.as_f64()).unwrap_or(0.0)
}
fn js_s(o: &JsValue, k: &str) -> String {
    js_sys::Reflect::get(o, &k.into()).ok().and_then(|v| v.as_string()).unwrap_or_default()
}
fn js_arr(o: &JsValue, k: &str) -> Vec<JsValue> {
    js_sys::Reflect::get(o, &k.into())
        .ok()
        .and_then(|v| v.dyn_into::<js_sys::Array>().ok())
        .map(|a| a.iter().collect())
        .unwrap_or_default()
}
fn scene_mode() -> bool {
    win_get("__SCENE_MODE").is_truthy()
}
fn scene_gen() -> f64 {
    js_f(&win_get("__SCENE"), "gen")
}
fn read_part(o: &JsValue) -> ScenePart {
    let text = |v: &JsValue| SceneText { t: js_s(v, "t"), f: js_s(v, "f"), x: js_f(v, "x"), y: js_f(v, "y") };
    ScenePart {
        glyphs: js_arr(o, "glyphs").iter().map(text).collect(),
        words: js_arr(o, "words").iter().map(text).collect(),
        boxes: js_arr(o, "boxes")
            .iter()
            .map(|v| SceneBox { x: js_f(v, "x"), y: js_f(v, "y"), w: js_f(v, "w"), h: js_f(v, "h"), r: js_f(v, "r") })
            .collect(),
    }
}
fn read_scene() -> Option<Scene> {
    let o = win_get("__SCENE");
    if !o.is_object() {
        return None;
    }
    let get = |k: &str| js_sys::Reflect::get(&o, &k.into()).unwrap_or(JsValue::UNDEFINED);
    Some(Scene {
        gen: js_f(&o, "gen"),
        vw: js_f(&o, "vw").max(1.0),
        vh: js_f(&o, "vh").max(1.0),
        page: read_part(&get("page")),
        chrome: read_part(&get("chrome")),
    })
}
// the slide content.js is animating: (offset in screen heights, NDC/s velocity, opacity)
fn scene_t() -> (f32, f32, f32) {
    let o = win_get("__SCENE_T");
    if !o.is_object() {
        return (0.0, 0.0, 1.0);
    }
    let op = js_sys::Reflect::get(&o, &"op".into()).ok().and_then(|v| v.as_f64()).unwrap_or(1.0);
    (js_f(&o, "dv") as f32, (js_f(&o, "vy") as f32).clamp(-8.0, 8.0), op.clamp(0.0, 1.0) as f32)
}

fn round_rect(ctx: &web_sys::CanvasRenderingContext2d, x: f64, y: f64, w: f64, h: f64, r: f64) {
    let r = r.min(w * 0.5).min(h * 0.5).max(0.0);
    ctx.begin_path();
    ctx.move_to(x + r, y);
    ctx.arc_to(x + w, y, x + w, y + h, r).ok();
    ctx.arc_to(x + w, y + h, x, y + h, r).ok();
    ctx.arc_to(x, y + h, x, y, r).ok();
    ctx.arc_to(x, y, x + w, y, r).ok();
    ctx.close_path();
    ctx.fill();
}

// a word's halo: its measured line box, padded
fn word_rect(ctx: &web_sys::CanvasRenderingContext2d, w: &SceneText) -> (f64, f64, f64, f64) {
    ctx.set_font(&w.f);
    let (width, asc, desc) = ctx
        .measure_text(&w.t)
        .map(|m| (m.width(), m.font_bounding_box_ascent(), m.font_bounding_box_descent()))
        .unwrap_or((0.0, 0.0, 0.0));
    let pad = 5.0;
    (w.x - pad, w.y - asc - pad, width + pad * 2.0, asc + desc + pad * 2.0)
}

// draw one part of the scene into a w x h canvas spanning the viewport, hard
// edged, in two colour channels: R = the glyphs (the type the GPU draws),
// G = everything solid (glyphs, pills, panels, media). One readback for both.
fn draw_part(ctx: &web_sys::CanvasRenderingContext2d, w: u32, h: u32, sc: &Scene, part: &ScenePart) -> Vec<u8> {
    let (wf, hf) = (w as f64, h as f64);
    ctx.set_transform(1.0, 0.0, 0.0, 1.0, 0.0, 0.0).ok();
    ctx.set_filter("none");
    ctx.set_global_composite_operation("source-over").ok();
    ctx.set_fill_style_str("#000000");
    ctx.fill_rect(0.0, 0.0, wf, hf);
    ctx.set_global_composite_operation("lighter").ok();
    ctx.set_text_align("left");
    ctx.set_text_baseline("alphabetic");
    // CSS px → canvas px (the canvas spans the viewport, whatever its aspect)
    ctx.set_transform(wf / sc.vw, 0.0, 0.0, hf / sc.vh, 0.0, 0.0).ok();
    ctx.set_fill_style_str("#ffff00");
    for g in &part.glyphs {
        ctx.set_font(&g.f);
        ctx.fill_text(&g.t, g.x, g.y).ok();
    }
    ctx.set_fill_style_str("#00ff00");
    for wd in &part.words {
        let (x, y, ww, hh) = word_rect(ctx, wd);
        round_rect(ctx, x, y, ww, hh, hh * 0.5);
    }
    for b in &part.boxes {
        round_rect(ctx, b.x, b.y, b.w, b.h, b.r);
    }
    ctx.set_transform(1.0, 0.0, 0.0, 1.0, 0.0, 0.0).ok();
    ctx.set_global_composite_operation("source-over").ok();
    // keep the edges empty: a sliding page samples past them (clamp-to-edge)
    ctx.set_fill_style_str("#000000");
    ctx.fill_rect(0.0, 0.0, wf, 2.0);
    ctx.fill_rect(0.0, hf - 2.0, wf, 2.0);
    ctx.fill_rect(0.0, 0.0, 2.0, hf);
    ctx.fill_rect(wf - 2.0, 0.0, 2.0, hf);
    ctx.get_image_data(0.0, 0.0, wf, hf).unwrap().data().to_vec()
}

fn channel(img: &[u8], c: usize) -> Vec<u8> {
    img.chunks_exact(4).map(|p| p[c]).collect()
}

// three box-blur passes each way ≈ a gaussian (sigma² ≈ r(r+1)), in place of
// canvas filters, which cost ~150 ms a scene on the CPU-backed field canvas
fn blur3(src: &[u8], w: usize, h: usize, r: usize) -> Vec<u8> {
    let mut a: Vec<u16> = src.iter().map(|&v| v as u16).collect();
    let mut b = vec![0u16; w * h];
    let d = (2 * r + 1) as u32;
    for _ in 0..3 {
        for y in 0..h {
            let row = &a[y * w..(y + 1) * w];
            let mut acc: u32 = 0;
            for x in 0..=r.min(w - 1) {
                acc += row[x] as u32;
            }
            acc += row[0] as u32 * r as u32; // edge clamp on the left
            for x in 0..w {
                b[y * w + x] = (acc / d) as u16;
                let add = row[(x + r + 1).min(w - 1)] as u32;
                let sub = row[x.saturating_sub(r)] as u32;
                acc = acc + add - sub;
            }
        }
        for x in 0..w {
            let mut acc: u32 = 0;
            for y in 0..=r.min(h - 1) {
                acc += b[y * w + x] as u32;
            }
            acc += b[x] as u32 * r as u32;
            for y in 0..h {
                a[y * w + x] = (acc / d) as u16;
                let add = b[(y + r + 1).min(h - 1) * w + x] as u32;
                let sub = b[y.saturating_sub(r) * w + x] as u32;
                acc = acc + add - sub;
            }
        }
    }
    a.iter().map(|&v| v.min(255) as u8).collect()
}

// the physics channel from a solid mask: solid inside, a soft halo outside.
// The halo is soft, so it's blurred at quarter resolution and scaled back up.
fn halo(solid: &[u8], w: u32, h: u32, k: f64) -> Vec<u8> {
    const F: usize = 4;
    let (w, h) = (w as usize, h as usize);
    let (lw, lh) = ((w + F - 1) / F, (h + F - 1) / F);
    let mut low = vec![0u32; lw * lh];
    for y in 0..h {
        let ly = (y / F) * lw;
        let row = &solid[y * w..(y + 1) * w];
        for (x, &v) in row.iter().enumerate() {
            low[ly + x / F] += v as u32;
        }
    }
    let low: Vec<u8> = low.iter().map(|&v| (v / (F * F) as u32).min(255) as u8).collect();
    let r = (((6.0 * k) / F as f64).round() as usize).clamp(1, 4);
    let soft = blur3(&low, lw, lh, r);
    // bilinear back up to full resolution
    let xs: Vec<(usize, usize, u32)> = (0..w)
        .map(|x| {
            let fx = ((x as f32 + 0.5) / F as f32 - 0.5).max(0.0);
            let x0 = (fx as usize).min(lw - 1);
            (x0, (x0 + 1).min(lw - 1), ((fx - x0 as f32) * 256.0) as u32)
        })
        .collect();
    let mut out = vec![0u8; w * h];
    for y in 0..h {
        let fy = ((y as f32 + 0.5) / F as f32 - 0.5).max(0.0);
        let y0 = (fy as usize).min(lh - 1);
        let y1 = (y0 + 1).min(lh - 1);
        let wy = ((fy - y0 as f32) * 256.0) as u32;
        let (r0, r1) = (&soft[y0 * lw..(y0 + 1) * lw], &soft[y1 * lw..(y1 + 1) * lw]);
        let o = &mut out[y * w..(y + 1) * w];
        let c = &solid[y * w..(y + 1) * w];
        for x in 0..w {
            let (x0, x1, wx) = xs[x];
            let top = r0[x0] as u32 * (256 - wx) + r0[x1] as u32 * wx;
            let bot = r1[x0] as u32 * (256 - wx) + r1[x1] as u32 * wx;
            let v = (top * (256 - wy) + bot * wy) >> 16;
            o[x] = (v * 4 / 3).min(255).max(c[x] as u32) as u8;
        }
    }
    out
}

// what the chrome looks like, to skip re-rastering it (it changes on a route
// change or resize, not on a page step)
fn part_key(sc: &Scene, part: &ScenePart) -> String {
    let mut k = format!("{:.0}x{:.0}", sc.vw, sc.vh);
    for g in part.glyphs.iter().chain(&part.words) {
        k.push_str(&format!("|{}@{}:{:.1},{:.1}", g.t, g.f, g.x, g.y));
    }
    for b in &part.boxes {
        k.push_str(&format!("|{:.1},{:.1},{:.1},{:.1},{:.1}", b.x, b.y, b.w, b.h, b.r));
    }
    k
}

// the field texture for a scene: page in RG, chrome in BA
fn raster_scene(
    ctx: &web_sys::CanvasRenderingContext2d,
    w: u32,
    h: u32,
    sc: &Scene,
    chrome_cache: &mut Option<(String, Vec<u8>, Vec<u8>)>,
) -> Vec<u8> {
    let k = w as f64 / sc.vw;
    let key = part_key(sc, &sc.chrome);
    if chrome_cache.as_ref().map(|c| c.0 != key).unwrap_or(true) {
        let chrome = draw_part(ctx, w, h, sc, &sc.chrome);
        let (cs, csolid) = (channel(&chrome, 0), channel(&chrome, 1));
        *chrome_cache = Some((key, halo(&csolid, w, h, k), cs));
    }
    let (_, cb, cs) = chrome_cache.as_ref().unwrap();
    let page = draw_part(ctx, w, h, sc, &sc.page);
    let psolid = channel(&page, 1);
    let pb = halo(&psolid, w, h, k);
    let mut out = Vec::with_capacity(page.len());
    for i in 0..pb.len() {
        out.extend_from_slice(&[pb[i], page[i * 4], cb[i], cs[i]]);
    }
    out
}

// the wake SDF of the page (what a sliding page plows with)
fn scene_sdf(ctx: &web_sys::CanvasRenderingContext2d, w: u32, h: u32, sc: &Scene, maxdist: f32) -> Vec<u8> {
    let solid = channel(&draw_part(ctx, w, h, sc, &sc.page), 1);
    coverage_to_sdf(&solid, w, h, w as f32, maxdist)
}

#[wasm_bindgen(start)]
pub fn start() {
    console_error_panic_hook::set_once();
    wasm_bindgen_futures::spawn_local(run());
}

async fn run() {
    let window = web_sys::window().unwrap();
    // dials.json is the single source of truth for tuned defaults: embedded at
    // compile time (cargo rebuilds when it changes), parsed once here, pushed
    // to the panel (which overlays localStorage), and used as dial() fallbacks
    let baked = js_sys::JSON::parse(include_str!("../dials.json"))
        .unwrap_or(wasm_bindgen::JsValue::NULL);
    let bk = {
        let baked = baked.clone();
        move |name: &str, fallback: f32| -> f32 {
            js_sys::Reflect::get(&baked, &name.into())
                .ok()
                .and_then(|v| v.as_f64())
                .map(|v| v as f32)
                .unwrap_or(fallback)
        }
    };
    if let Ok(f) = js_sys::Reflect::get(&window, &"__initDials".into()) {
        if let Some(func) = f.dyn_ref::<js_sys::Function>() {
            func.call1(&wasm_bindgen::JsValue::NULL, &baked).ok();
        }
    }
    // phrases.json: baked phrase list, pushed to the editor panel (which edits it as a
    // JSON string array → window.__PHRASES). Falls back to a default if empty.
    let baked_phrases: Vec<String> = {
        let v = js_sys::JSON::parse(include_str!("../phrases.json"))
            .unwrap_or(wasm_bindgen::JsValue::NULL);
        if let Ok(f) = js_sys::Reflect::get(&window, &"__initPhrases".into()) {
            if let Some(func) = f.dyn_ref::<js_sys::Function>() {
                func.call1(&wasm_bindgen::JsValue::NULL, &v).ok();
            }
        }
        let mut ph = Vec::new();
        if let Ok(arr) = v.dyn_into::<js_sys::Array>() {
            for i in 0..arr.length() {
                if let Some(p) = phrase_entry(&arr.get(i)) { ph.push(p); }
            }
        }
        if ph.is_empty() { ph.push("BUILDS TECHNOLOGY".to_string()); }
        ph
    };
    let document = window.document().unwrap();
    let canvas: web_sys::HtmlCanvasElement =
        document.get_element_by_id("canvas").unwrap().dyn_into().unwrap();
    // render at device resolution (capped 2x) for retina crispness; CSS keeps
    // the canvas at viewport size
    let dpr = window.device_pixel_ratio().min(2.0);
    let css_w = window.inner_width().unwrap().as_f64().unwrap();
    let css_h = window.inner_height().unwrap().as_f64().unwrap();
    // embedded previews can load the page while the viewport is still 0-sized;
    // initializing against that poisons every GPU resource — retry instead
    if css_w < 50.0 || css_h < 50.0 {
        // re-run in place rather than reloading: embedded, the page's signed
        // URL is only valid for 60s, so a reload can 403
        let retry = Closure::<dyn FnMut()>::new(move || {
            wasm_bindgen_futures::spawn_local(run());
        });
        window
            .set_timeout_with_callback_and_timeout_and_arguments_0(
                retry.as_ref().unchecked_ref(),
                250,
            )
            .ok();
        retry.forget();
        set_status("waiting for viewport…");
        return;
    }
    let width = (css_w * dpr) as u32;
    let height = (css_h * dpr) as u32;
    // phones get a calmer stream: 500k reads as overwhelming at that scale
    let particle_count: u32 = if css_w < 700.0 { 200_000 } else { PARTICLES };
    canvas.set_width(width);
    canvas.set_height(height);
    let aspect = width as f32 / height as f32;

    // a fully procedural page can simply reload on resize (debounced) — but
    // only standalone: embedded, the signed index URL expires after 60s, so a
    // reload would 403. There the canvas just stretches to the new viewport.
    if standalone() {
        let win2 = window.clone();
        let pending = Rc::new(Cell::new(0i32));
        let pend2 = pending.clone();
        let reload = Closure::<dyn FnMut()>::new(move || {
            if let Some(w) = web_sys::window() { w.location().reload().ok(); }
        });
        let cb = Closure::<dyn FnMut(web_sys::Event)>::new(move |_e: web_sys::Event| {
            let id = win2
                .set_timeout_with_callback_and_timeout_and_arguments_0(
                    reload.as_ref().unchecked_ref(), 350)
                .unwrap_or(0);
            let prev = pend2.replace(id);
            if prev != 0 { win2.clear_timeout_with_handle(prev); }
        });
        window
            .add_event_listener_with_callback("resize", cb.as_ref().unchecked_ref())
            .unwrap();
        cb.forget();
    }

    // offscreen 2D canvas for the obstacle field (reused on every phrase swap)
    let field_w = FIELD_W;
    let field_h = (((field_w as f32 / aspect) as u32) + 3) & !3u32;
    let fcanvas: web_sys::HtmlCanvasElement =
        document.create_element("canvas").unwrap().dyn_into().unwrap();
    fcanvas.set_width(field_w);
    fcanvas.set_height(field_h);
    // read back on every scene swap: keep it CPU-side (willReadFrequently)
    let rf = js_sys::Object::new();
    js_sys::Reflect::set(&rf, &"willReadFrequently".into(), &true.into()).ok();
    let fctx: web_sys::CanvasRenderingContext2d = fcanvas
        .get_context_with_context_options("2d", &rf)
        .unwrap()
        .unwrap()
        .dyn_into()
        .unwrap();

    // mouse/touch → NDC, drives the particle PERTURBATION (the primary interaction now
    // that dragging/nav is parked). `drag`.2 is just "a finger/mouse is down" → press wake.
    let mouse = Rc::new(Cell::new((0.0f32, 0.0f32, 0.0f32))); // x, y, active
    let drag = Rc::new(Cell::new((0.0f32, 0.0f32, 0.0f32))); // _, _, pressed
    // mouse move → perturb the stream (a finger through the water)
    {
        let m = mouse.clone();
        let (w, h) = (css_w as f32, css_h as f32);
        let cb = Closure::<dyn FnMut(web_sys::MouseEvent)>::new(move |e: web_sys::MouseEvent| {
            let x = (e.client_x() as f32 / w) * 2.0 - 1.0;
            let y = -((e.client_y() as f32 / h) * 2.0 - 1.0);
            m.set((x, y, 1.0));
        });
        window.add_event_listener_with_callback("mousemove", cb.as_ref().unchecked_ref()).unwrap();
        cb.forget();
    }
    // mouse down / up → press wake (pressed flag only)
    {
        let dr = drag.clone();
        let cb = Closure::<dyn FnMut(web_sys::MouseEvent)>::new(move |_e: web_sys::MouseEvent| {
            let d = dr.get(); dr.set((d.0, d.1, 1.0));
        });
        canvas.add_event_listener_with_callback("mousedown", cb.as_ref().unchecked_ref()).unwrap();
        cb.forget();
    }
    {
        let dr = drag.clone();
        let cb = Closure::<dyn FnMut(web_sys::MouseEvent)>::new(move |_e: web_sys::MouseEvent| {
            let d = dr.get(); dr.set((d.0, d.1, 0.0));
        });
        window.add_event_listener_with_callback("mouseup", cb.as_ref().unchecked_ref()).unwrap();
        cb.forget();
    }
    {
        let m = mouse.clone();
        let cb = Closure::<dyn FnMut(web_sys::MouseEvent)>::new(move |_e: web_sys::MouseEvent| {
            let (x, y, _) = m.get(); m.set((x, y, 0.0));
        });
        document.add_event_listener_with_callback("mouseleave", cb.as_ref().unchecked_ref()).unwrap();
        cb.forget();
    }
    // touch → perturb (position) + press wake; no dragging
    {
        let m = mouse.clone();
        let dr = drag.clone();
        let (w, h) = (css_w as f32, css_h as f32);
        let cb = Closure::<dyn FnMut(web_sys::TouchEvent)>::new(move |e: web_sys::TouchEvent| {
            if let Some(t) = e.touches().get(0) {
                let x = (t.client_x() as f32 / w) * 2.0 - 1.0;
                let y = -((t.client_y() as f32 / h) * 2.0 - 1.0);
                m.set((x, y, 1.0));
                let d = dr.get(); dr.set((d.0, d.1, 1.0));
            }
        });
        let r = cb.as_ref().unchecked_ref();
        canvas.add_event_listener_with_callback("touchstart", r).unwrap();
        window.add_event_listener_with_callback("touchmove", r).unwrap();
        cb.forget();
    }
    {
        let m = mouse.clone();
        let dr = drag.clone();
        let cb = Closure::<dyn FnMut(web_sys::TouchEvent)>::new(move |_e: web_sys::TouchEvent| {
            let (x, y, _) = m.get(); m.set((x, y, 0.0));
            let d = dr.get(); dr.set((d.0, d.1, 0.0));
        });
        let r = cb.as_ref().unchecked_ref();
        window.add_event_listener_with_callback("touchend", r).unwrap();
        window.add_event_listener_with_callback("touchcancel", r).unwrap();
        cb.forget();
    }

    // ---- wgpu ----
    let instance = wgpu::Instance::default();
    let surface = instance
        .create_surface(wgpu::SurfaceTarget::Canvas(canvas))
        .unwrap();
    let adapter = match instance
        .request_adapter(&wgpu::RequestAdapterOptions {
            power_preference: wgpu::PowerPreference::HighPerformance,
            force_fallback_adapter: false,
            compatible_surface: Some(&surface),
        })
        .await
    {
        Ok(a) => a,
        Err(e) => {
            set_status(&format!("WebGPU adapter UNAVAILABLE: {e}"));
            site_error(&format!("WebGPU adapter unavailable: {e}"), "error");
            return;
        }
    };
    let (device, queue) = match adapter
        .request_device(&wgpu::DeviceDescriptor {
            label: None,
            required_features: wgpu::Features::empty(),
            required_limits: wgpu::Limits::default(),
            memory_hints: wgpu::MemoryHints::default(),
            experimental_features: wgpu::ExperimentalFeatures::default(),
            trace: wgpu::Trace::Off,
        })
        .await
    {
        Ok(dq) => dq,
        Err(e) => {
            set_status(&format!("WebGPU device UNAVAILABLE: {e}"));
            site_error(&format!("WebGPU request_device failed: {e}"), "gpu-lost");
            return;
        }
    };
    device.set_device_lost_callback(|reason, msg| {
        if reason != wgpu::DeviceLostReason::Destroyed {
            set_status("WebGPU device lost");
            site_error(&format!("WebGPU device lost: {msg}"), "gpu-lost");
        }
    });

    let caps = surface.get_capabilities(&adapter);
    let format = caps.formats[0];
    let config = wgpu::SurfaceConfiguration {
        usage: wgpu::TextureUsages::RENDER_ATTACHMENT,
        format,
        width,
        height,
        present_mode: wgpu::PresentMode::Fifo,
        desired_maximum_frame_latency: 2,
        alpha_mode: caps.alpha_modes[0],
        view_formats: vec![],
    };
    surface.configure(&device, &config);

    let sim_mod = device.create_shader_module(wgpu::ShaderModuleDescriptor {
        label: Some("sim"),
        source: wgpu::ShaderSource::Wgsl(SIM_SHADER.into()),
    });
    let draw_mod = device.create_shader_module(wgpu::ShaderModuleDescriptor {
        label: Some("draw"),
        source: wgpu::ShaderSource::Wgsl(DRAW_SHADER.into()),
    });
    let post_mod = device.create_shader_module(wgpu::ShaderModuleDescriptor {
        label: Some("post"),
        source: wgpu::ShaderSource::Wgsl(POST_SHADER.into()),
    });

    // ---- buffers & textures ----
    let mut rng = 0x9e3779b9u32;
    let mut init: Vec<f32> = Vec::with_capacity(particle_count as usize * 4);
    // intro: seed across the WHOLE screen so the field is fully developed from
    // the first frame and simply fades in (no waiting for particles to flow in)
    for _ in 0..particle_count {
        init.push(rnd(&mut rng) * 2.2 - 1.1);          // pos.x in [-1.1, 1.1]
        init.push(rnd(&mut rng) * 2.2 - 1.1);          // pos.y in [-1.1, 1.1]
        init.push((rnd(&mut rng) - 0.5) * 0.3);        // vel.x small (flow goal takes over within ~0.4s)
        init.push((rnd(&mut rng) - 0.5) * 0.3);        // vel.y small
    }
    let particle_buf = device.create_buffer(&wgpu::BufferDescriptor {
        label: Some("particles"),
        size: (particle_count as u64) * 16,
        usage: wgpu::BufferUsages::STORAGE
            | wgpu::BufferUsages::VERTEX
            | wgpu::BufferUsages::COPY_DST,
        mapped_at_creation: false,
    });
    queue.write_buffer(&particle_buf, 0, bytemuck::cast_slice(&init));

    let param_buf = device.create_buffer(&wgpu::BufferDescriptor {
        label: Some("params"),
        size: std::mem::size_of::<Params>() as u64,
        usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
        mapped_at_creation: false,
    });

    // four-channel field: name in RG (R blur/physics, G sharp/type), phrase in
    // BA (B blur/physics, A sharp/type) — independent layers so the name stays
    // solid while the phrase fades + pushes in z
    let field_tex = device.create_texture(&wgpu::TextureDescriptor {
        label: Some("field"),
        size: wgpu::Extent3d {
            width: field_w,
            height: field_h,
            depth_or_array_layers: 1,
        },
        mip_level_count: 1,
        sample_count: 1,
        dimension: wgpu::TextureDimension::D2,
        format: wgpu::TextureFormat::Rgba8Unorm,
        usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
        view_formats: &[],
    });
    let field_view = field_tex.create_view(&wgpu::TextureViewDescriptor::default());
    let lin_samp = device.create_sampler(&wgpu::SamplerDescriptor {
        label: Some("linear-clamp"),
        address_mode_u: wgpu::AddressMode::ClampToEdge,
        address_mode_v: wgpu::AddressMode::ClampToEdge,
        mag_filter: wgpu::FilterMode::Linear,
        min_filter: wgpu::FilterMode::Linear,
        ..Default::default()
    });

    // HDR scene buffer + half-res bloom ping-pong
    let hdr_fmt = wgpu::TextureFormat::Rgba16Float;
    let mk_target = |label: &str, w: u32, h: u32| {
        device.create_texture(&wgpu::TextureDescriptor {
            label: Some(label),
            size: wgpu::Extent3d { width: w, height: h, depth_or_array_layers: 1 },
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: hdr_fmt,
            usage: wgpu::TextureUsages::RENDER_ATTACHMENT
                | wgpu::TextureUsages::TEXTURE_BINDING,
            view_formats: &[],
        })
    };
    let scene_tex = mk_target("scene", width, height);
    let bw = (width / 2).max(1);
    let bh = (height / 2).max(1);
    let bloom_a = mk_target("bloomA", bw, bh);
    let bloom_b = mk_target("bloomB", bw, bh);
    let scene_view = scene_tex.create_view(&wgpu::TextureViewDescriptor::default());
    let bloom_a_view = bloom_a.create_view(&wgpu::TextureViewDescriptor::default());
    let bloom_b_view = bloom_b.create_view(&wgpu::TextureViewDescriptor::default());

    fn upload_field(
        queue: &wgpu::Queue,
        tex: &wgpu::Texture,
        fw: u32,
        fh: u32,
        bytes: &[u8],
    ) {
        queue.write_texture(
            wgpu::TexelCopyTextureInfo {
                texture: tex,
                mip_level: 0,
                origin: wgpu::Origin3d::ZERO,
                aspect: wgpu::TextureAspect::All,
            },
            bytes,
            wgpu::TexelCopyBufferLayout {
                offset: 0,
                bytes_per_row: Some(fw * 4),
                rows_per_image: Some(fh),
            },
            wgpu::Extent3d {
                width: fw,
                height: fh,
                depth_or_array_layers: 1,
            },
        );
    }
    // name field is computed ONCE (it never changes); phrase field is rebuilt
    // on each swap and interleaved with the cached name channels
    let name_entries = name_layout(field_w, field_h, css_w);
    let (name_blur, name_sharp) = raster_layer(&fctx, field_w, field_h, &name_entries);
    let first_phrase = current_action_phrases(&baked_phrases)
        .into_iter()
        .next()
        .unwrap_or_else(|| baked_phrases[0].clone());
    // the (parked, inert) menu atlas still bakes at setup — feed it the first phrase
    // so it compiles; menu_du is off-screen so it never shows.
    let init_east = first_phrase.clone();
    let (p_entries0, phrase_cy0) = phrase_layout(&fctx, field_w, field_h, css_w, &first_phrase);
    let (pb0, ps0) = raster_layer(&fctx, field_w, field_h, &p_entries0);
    upload_field(
        &queue,
        &field_tex,
        field_w,
        field_h,
        &pack_rgba(&name_blur, &name_sharp, &pb0, &ps0),
    );

    // MENU + placeholder map directions baked ONCE into a 3x3 world atlas:
    // centre cell empty (the name shows from the field), 8 fixed panels around
    // parked (menu_du = 5: always off screen): blank, not rastered
    let menu_zero = vec![0u8; (field_w * field_h) as usize];
    let _ = &init_east;
    let menu_tex = device.create_texture(&wgpu::TextureDescriptor {
        label: Some("menu"),
        size: wgpu::Extent3d { width: field_w, height: field_h, depth_or_array_layers: 1 },
        mip_level_count: 1,
        sample_count: 1,
        dimension: wgpu::TextureDimension::D2,
        format: wgpu::TextureFormat::Rgba8Unorm,
        usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
        view_formats: &[],
    });
    let menu_view = menu_tex.create_view(&wgpu::TextureViewDescriptor::default());
    upload_field(&queue, &menu_tex, field_w, field_h,
        &pack_rgba(&menu_zero, &menu_zero, &menu_zero, &menu_zero));

    // WAKE SDF: a low-res distance field of name+phrase (RG=outward dir, B=dist),
    // re-baked per phrase at runtime so it always matches the responsive layout.
    // Lazy cache + pre-warm live in the frame loop; this seeds the first phrase.
    const SDF_MAXDIST: f32 = 0.35;
    let sdf_w = 384u32;
    let sdf_h = (((sdf_w as f32 * field_h as f32 / field_w as f32) as u32) + 3) & !3u32;
    let scanvas: web_sys::HtmlCanvasElement =
        document.create_element("canvas").unwrap().dyn_into().unwrap();
    scanvas.set_width(sdf_w);
    scanvas.set_height(sdf_h);
    let sctx: web_sys::CanvasRenderingContext2d = scanvas
        .get_context_with_context_options("2d", &rf)
        .unwrap()
        .unwrap()
        .dyn_into()
        .unwrap();
    // SECTION CAMERA: bake the (prototype) section title once → its stroke-
    // centerline path (the camera spine) + a wake-format SDF (rendered crisp when
    // scaled up, and later fed to the wake). Square raster so path and SDF share
    // isotropic coords.
    // parked (titlex.w = 0 every frame): a blank 4x4 instead of a 2048² bake,
    // which cost the first frame a full-res distance transform
    let (title_w, title_h) = (4u32, 4u32);
    let title_sdf_bytes = {
        let pcanvas: web_sys::HtmlCanvasElement =
            document.create_element("canvas").unwrap().dyn_into().unwrap();
        pcanvas.set_width(title_w);
        pcanvas.set_height(title_h);
        let pctx: web_sys::CanvasRenderingContext2d =
            pcanvas.get_context("2d").unwrap().unwrap().dyn_into().unwrap();
        let _ = &pctx;
        vec![0u8; (title_w * title_h * 4) as usize]
    };
    let title_sdf_tex = device.create_texture(&wgpu::TextureDescriptor {
        label: Some("title-sdf"),
        size: wgpu::Extent3d { width: title_w, height: title_h, depth_or_array_layers: 1 },
        mip_level_count: 1,
        sample_count: 1,
        dimension: wgpu::TextureDimension::D2,
        format: wgpu::TextureFormat::Rgba8Unorm,
        usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
        view_formats: &[],
    });
    let title_sdf_view = title_sdf_tex.create_view(&wgpu::TextureViewDescriptor::default());
    upload_field(&queue, &title_sdf_tex, title_w, title_h, &title_sdf_bytes);
    // title transform uniform (own buffer, NOT in Params): [scale, off_x, off_y, on]
    let title_buf = device.create_buffer(&wgpu::BufferDescriptor {
        label: Some("title-xform"),
        size: 16,
        usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
        mapped_at_creation: false,
    });
    let sdf_tex = device.create_texture(&wgpu::TextureDescriptor {
        label: Some("wake-sdf"),
        size: wgpu::Extent3d { width: sdf_w, height: sdf_h, depth_or_array_layers: 1 },
        mip_level_count: 1,
        sample_count: 1,
        dimension: wgpu::TextureDimension::D2,
        format: wgpu::TextureFormat::Rgba8Unorm,
        usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
        view_formats: &[],
    });
    let sdf_view = sdf_tex.create_view(&wgpu::TextureViewDescriptor::default());
    let mut sdf_cache: std::collections::HashMap<String, Vec<u8>> = std::collections::HashMap::new();
    sdf_cache.insert(
        first_phrase.clone(),
        bake_sdf(&sctx, sdf_w, sdf_h, css_w, &first_phrase, SDF_MAXDIST),
    );
    upload_field(&queue, &sdf_tex, sdf_w, sdf_h, sdf_cache.get(&first_phrase).unwrap());

    // wake SDF for the off-screen panel atlas — STATIC, so baked once. Sampled in
    // the panned atlas frame + masked to the active cell, so the wake follows only
    // the panel that's animating in.
    let menu_sdf_tex = device.create_texture(&wgpu::TextureDescriptor {
        label: Some("menu-sdf"),
        size: wgpu::Extent3d { width: sdf_w, height: sdf_h, depth_or_array_layers: 1 },
        mip_level_count: 1,
        sample_count: 1,
        dimension: wgpu::TextureDimension::D2,
        format: wgpu::TextureFormat::Rgba8Unorm,
        usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
        view_formats: &[],
    });
    let menu_sdf_view = menu_sdf_tex.create_view(&wgpu::TextureViewDescriptor::default());
    // parked too (pad0 = 9: never sampled)
    upload_field(&queue, &menu_sdf_tex, sdf_w, sdf_h, &vec![0u8; (sdf_w * sdf_h * 4) as usize]);

    // ---- bind group layouts ----
    let common_bgl = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
        label: Some("common"),
        entries: &[
            wgpu::BindGroupLayoutEntry {
                binding: 0,
                visibility: wgpu::ShaderStages::COMPUTE
                    | wgpu::ShaderStages::VERTEX
                    | wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Buffer {
                    ty: wgpu::BufferBindingType::Uniform,
                    has_dynamic_offset: false,
                    min_binding_size: None,
                },
                count: None,
            },
            wgpu::BindGroupLayoutEntry {
                binding: 1,
                visibility: wgpu::ShaderStages::COMPUTE | wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Texture {
                    sample_type: wgpu::TextureSampleType::Float { filterable: true },
                    view_dimension: wgpu::TextureViewDimension::D2,
                    multisampled: false,
                },
                count: None,
            },
            wgpu::BindGroupLayoutEntry {
                binding: 2,
                visibility: wgpu::ShaderStages::COMPUTE | wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                count: None,
            },
            wgpu::BindGroupLayoutEntry {
                binding: 3,
                visibility: wgpu::ShaderStages::COMPUTE | wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Texture {
                    sample_type: wgpu::TextureSampleType::Float { filterable: true },
                    view_dimension: wgpu::TextureViewDimension::D2,
                    multisampled: false,
                },
                count: None,
            },
            wgpu::BindGroupLayoutEntry {
                binding: 4, // name+phrase wake SDF — compute-only (the SIM samples it)
                visibility: wgpu::ShaderStages::COMPUTE,
                ty: wgpu::BindingType::Texture {
                    sample_type: wgpu::TextureSampleType::Float { filterable: true },
                    view_dimension: wgpu::TextureViewDimension::D2,
                    multisampled: false,
                },
                count: None,
            },
            wgpu::BindGroupLayoutEntry {
                binding: 5, // off-screen panel wake SDF — compute-only
                visibility: wgpu::ShaderStages::COMPUTE,
                ty: wgpu::BindingType::Texture {
                    sample_type: wgpu::TextureSampleType::Float { filterable: true },
                    view_dimension: wgpu::TextureViewDimension::D2,
                    multisampled: false,
                },
                count: None,
            },
        ],
    });
    let parts_bgl = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
        label: Some("parts"),
        entries: &[wgpu::BindGroupLayoutEntry {
            binding: 0,
            visibility: wgpu::ShaderStages::COMPUTE,
            ty: wgpu::BindingType::Buffer {
                ty: wgpu::BufferBindingType::Storage { read_only: false },
                has_dynamic_offset: false,
                min_binding_size: None,
            },
            count: None,
        }],
    });
    // one texture + sampler (bright/blur passes)
    let tex_entry = |binding: u32| wgpu::BindGroupLayoutEntry {
        binding,
        visibility: wgpu::ShaderStages::FRAGMENT,
        ty: wgpu::BindingType::Texture {
            sample_type: wgpu::TextureSampleType::Float { filterable: true },
            view_dimension: wgpu::TextureViewDimension::D2,
            multisampled: false,
        },
        count: None,
    };
    let post_bgl = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
        label: Some("post"),
        entries: &[
            tex_entry(0),
            wgpu::BindGroupLayoutEntry {
                binding: 1,
                visibility: wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                count: None,
            },
            tex_entry(3),
        ],
    });
    // scene + sampler + bloom (composite)
    let comp_bgl = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
        label: Some("comp"),
        entries: &[
            tex_entry(0),
            wgpu::BindGroupLayoutEntry {
                binding: 1,
                visibility: wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                count: None,
            },
            tex_entry(2),
            tex_entry(3),
            wgpu::BindGroupLayoutEntry {
                binding: 4,
                visibility: wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Buffer {
                    ty: wgpu::BufferBindingType::Uniform,
                    has_dynamic_offset: false,
                    min_binding_size: None,
                },
                count: None,
            },
            tex_entry(5),
        ],
    });

    let common_bg = device.create_bind_group(&wgpu::BindGroupDescriptor {
        label: None,
        layout: &common_bgl,
        entries: &[
            wgpu::BindGroupEntry { binding: 0, resource: param_buf.as_entire_binding() },
            wgpu::BindGroupEntry { binding: 1, resource: wgpu::BindingResource::TextureView(&field_view) },
            wgpu::BindGroupEntry { binding: 2, resource: wgpu::BindingResource::Sampler(&lin_samp) },
            wgpu::BindGroupEntry { binding: 3, resource: wgpu::BindingResource::TextureView(&menu_view) },
            wgpu::BindGroupEntry { binding: 4, resource: wgpu::BindingResource::TextureView(&sdf_view) },
            wgpu::BindGroupEntry { binding: 5, resource: wgpu::BindingResource::TextureView(&menu_sdf_view) },
        ],
    });
    let parts_bg = device.create_bind_group(&wgpu::BindGroupDescriptor {
        label: None,
        layout: &parts_bgl,
        entries: &[wgpu::BindGroupEntry { binding: 0, resource: particle_buf.as_entire_binding() }],
    });
    let bright_bg = device.create_bind_group(&wgpu::BindGroupDescriptor {
        label: None,
        layout: &post_bgl,
        entries: &[
            wgpu::BindGroupEntry { binding: 0, resource: wgpu::BindingResource::TextureView(&scene_view) },
            wgpu::BindGroupEntry { binding: 1, resource: wgpu::BindingResource::Sampler(&lin_samp) },
            wgpu::BindGroupEntry { binding: 3, resource: wgpu::BindingResource::TextureView(&field_view) },
        ],
    });
    let blurv_bg = device.create_bind_group(&wgpu::BindGroupDescriptor {
        label: None,
        layout: &post_bgl,
        entries: &[
            wgpu::BindGroupEntry { binding: 0, resource: wgpu::BindingResource::TextureView(&bloom_a_view) },
            wgpu::BindGroupEntry { binding: 1, resource: wgpu::BindingResource::Sampler(&lin_samp) },
            wgpu::BindGroupEntry { binding: 3, resource: wgpu::BindingResource::TextureView(&field_view) },
        ],
    });
    let comp_bg = device.create_bind_group(&wgpu::BindGroupDescriptor {
        label: None,
        layout: &comp_bgl,
        entries: &[
            wgpu::BindGroupEntry { binding: 0, resource: wgpu::BindingResource::TextureView(&scene_view) },
            wgpu::BindGroupEntry { binding: 1, resource: wgpu::BindingResource::Sampler(&lin_samp) },
            wgpu::BindGroupEntry { binding: 2, resource: wgpu::BindingResource::TextureView(&bloom_b_view) },
            wgpu::BindGroupEntry { binding: 3, resource: wgpu::BindingResource::TextureView(&field_view) },
            wgpu::BindGroupEntry { binding: 4, resource: param_buf.as_entire_binding() },
            wgpu::BindGroupEntry { binding: 5, resource: wgpu::BindingResource::TextureView(&menu_view) },
        ],
    });

    // section-title group: own uniform + SDF + sampler. Wired into the composite
    // (group 1) now and the sim wake later — keeps Params untouched.
    let title_bgl = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
        label: Some("title"),
        entries: &[
            wgpu::BindGroupLayoutEntry {
                binding: 0,
                visibility: wgpu::ShaderStages::COMPUTE | wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Buffer {
                    ty: wgpu::BufferBindingType::Uniform,
                    has_dynamic_offset: false,
                    min_binding_size: None,
                },
                count: None,
            },
            wgpu::BindGroupLayoutEntry {
                binding: 1,
                visibility: wgpu::ShaderStages::COMPUTE | wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Texture {
                    sample_type: wgpu::TextureSampleType::Float { filterable: true },
                    view_dimension: wgpu::TextureViewDimension::D2,
                    multisampled: false,
                },
                count: None,
            },
            wgpu::BindGroupLayoutEntry {
                binding: 2,
                visibility: wgpu::ShaderStages::COMPUTE | wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                count: None,
            },
        ],
    });
    let title_bg = device.create_bind_group(&wgpu::BindGroupDescriptor {
        label: Some("title"),
        layout: &title_bgl,
        entries: &[
            wgpu::BindGroupEntry { binding: 0, resource: title_buf.as_entire_binding() },
            wgpu::BindGroupEntry { binding: 1, resource: wgpu::BindingResource::TextureView(&title_sdf_view) },
            wgpu::BindGroupEntry { binding: 2, resource: wgpu::BindingResource::Sampler(&lin_samp) },
        ],
    });

    let compute_pl = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
        label: None,
        bind_group_layouts: &[Some(&common_bgl), Some(&parts_bgl), Some(&title_bgl)],
        immediate_size: 0,
    });
    let render_pl = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
        label: None,
        bind_group_layouts: &[Some(&common_bgl)],
        immediate_size: 0,
    });
    let post_pl = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
        label: None,
        bind_group_layouts: &[Some(&post_bgl)],
        immediate_size: 0,
    });
    let comp_pl = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
        label: None,
        bind_group_layouts: &[Some(&comp_bgl), Some(&title_bgl)],
        immediate_size: 0,
    });

    // ---- pipelines ----
    let sim_pipeline = device.create_compute_pipeline(&wgpu::ComputePipelineDescriptor {
        label: Some("sim"),
        layout: Some(&compute_pl),
        module: &sim_mod,
        entry_point: Some("cs"),
        compilation_options: Default::default(),
        cache: None,
    });

    let mk_full = |label: &str,
                   module: &wgpu::ShaderModule,
                   vs: &str,
                   fs: &str,
                   layout: &wgpu::PipelineLayout,
                   target: wgpu::TextureFormat| {
        device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
            label: Some(label),
            layout: Some(layout),
            vertex: wgpu::VertexState {
                module,
                entry_point: Some(vs),
                buffers: &[],
                compilation_options: Default::default(),
            },
            fragment: Some(wgpu::FragmentState {
                module,
                entry_point: Some(fs),
                targets: &[Some(target.into())],
                compilation_options: Default::default(),
            }),
            primitive: wgpu::PrimitiveState::default(),
            depth_stencil: None,
            multisample: wgpu::MultisampleState::default(),
            multiview_mask: None,
            cache: None,
        })
    };
    let bg_pipeline = mk_full("bg", &draw_mod, "vs_bg", "fs_bg", &render_pl, hdr_fmt);
    let bright_pipeline = mk_full("bright", &post_mod, "vs_full", "fs_bright_h", &post_pl, hdr_fmt);
    let blurv_pipeline = mk_full("blurv", &post_mod, "vs_full", "fs_blur_v", &post_pl, hdr_fmt);
    let comp_pipeline = mk_full("comp", &post_mod, "vs_full", "fs_comp", &comp_pl, format);

    let attrs = wgpu::vertex_attr_array![0 => Float32x2, 1 => Float32x2];
    let p_pipeline = device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
        label: Some("particles"),
        layout: Some(&render_pl),
        vertex: wgpu::VertexState {
            module: &draw_mod,
            entry_point: Some("vs_p"),
            buffers: &[wgpu::VertexBufferLayout {
                array_stride: 16,
                step_mode: wgpu::VertexStepMode::Instance,
                attributes: &attrs,
            }],
            compilation_options: Default::default(),
        },
        fragment: Some(wgpu::FragmentState {
            module: &draw_mod,
            entry_point: Some("fs_p"),
            targets: &[Some(wgpu::ColorTargetState {
                format: hdr_fmt,
                blend: Some(wgpu::BlendState {
                    // additive light into the HDR buffer — bloom feeds on the sum
                    color: wgpu::BlendComponent {
                        src_factor: wgpu::BlendFactor::One,
                        dst_factor: wgpu::BlendFactor::One,
                        operation: wgpu::BlendOperation::Add,
                    },
                    alpha: wgpu::BlendComponent {
                        src_factor: wgpu::BlendFactor::Zero,
                        dst_factor: wgpu::BlendFactor::One,
                        operation: wgpu::BlendOperation::Add,
                    },
                }),
                write_mask: wgpu::ColorWrites::ALL,
            })],
            compilation_options: Default::default(),
        }),
        primitive: wgpu::PrimitiveState::default(),
        depth_stencil: None,
        multisample: wgpu::MultisampleState::default(),
        multiview_mask: None,
        cache: None,
    });

    // ---- frame loop ----
    let groups = (particle_count + WG - 1) / WG;
    let perf = window.performance().unwrap();
    let t0 = perf.now();
    let mut last = t0;
    let mut frames: u32 = 0;
    let mut acc: f64 = 0.0;
    let mut sim_t: f64 = 0.0; // intro clock on capped sim-time (stays synced to the fill at any fps)
    let mut press_z = 1.0f32; // visible-text recede: dips on press, eases back on release
    let mut phrase_idx: usize = 0;
    let mut seen_gen: f64 = phrases_gen();
    let mut phase: u8 = 0; // 0 hold, 1 exit (push back + fade), 2 enter (forward + fade in)
    let mut phase_start = t0;
    let mut phrase_cy = phrase_cy0 as f32;
    let mut presented = false; // site.ready() once the first frame is on screen
    let mut scene_seen: f64 = 0.0; // the __SCENE gen in the field now
    let mut scene_drawn = false; // __SCENE_DRAWN published
    let mut chrome_cache: Option<(String, Vec<u8>, Vec<u8>)> = None; // the nav + pager, rastered

    let f = Rc::new(RefCell::new(None::<Closure<dyn FnMut()>>));
    let g = f.clone();
    let win = window.clone();
    let mouse_r = mouse.clone();
    let drag_r = drag.clone();
    *g.borrow_mut() = Some(Closure::wrap(Box::new(move || {
        let now = perf.now();
        let dt_ms = now - last;
        last = now;
        let dt = (dt_ms / 1000.0).min(0.033) as f32;
        sim_t += dt as f64;
        frames += 1;
        acc += dt_ms;
        if acc >= 500.0 {
            set_status(&format!(
                "{:.0} fps · {}k particles",
                frames as f64 * 1000.0 / acc,
                particle_count / 1000
            ));
            frames = 0;
            acc = 0.0;
        }

        // phrase transition: hold → exit (fade + push back) → [swap at the
        // invisible point] → enter (fade in + push forward). phrase_tt is 0
        // when resting, 1 when fully gone/back.
        let it = sim_t as f32;
        let ss = |a: f32, b: f32, x: f32| -> f32 {
            let t = ((x - a) / (b - a)).clamp(0.0, 1.0);
            t * t * (3.0 - 2.0 * t)
        };
        let bg_fade = ss(0.0, 1.8, it);                  // background fades in alongside the particles
        let part_fade = ss(0.0, 1.8, it);                // particles fade in over the already-populated field
        let name_op = ss(0.9, 1.4, it);                  // text resolves early, over the inflow
        let intro_glow = 0.05 + 0.95 * ss(1.2, 3.3, it); // sparkle + bloom kept low through the sweep, ramp up after
        const INTRO_DUR: f32 = 1.8;
        let phrase_op: f32;
        let phrase_w: f32;
        let phrase_z: f32;
        // the site scene (content.js) replaces the phrase cycle: page in RG,
        // chrome in BA, re-rastered whenever the page publishes a new one
        let smode = scene_mode();
        let (s_dv, s_vy, s_op) = if smode { scene_t() } else { (0.0, 0.0, 1.0) };
        if smode {
            let g = scene_gen();
            if g > 0.0 && g != scene_seen {
                if let Some(sc) = read_scene() {
                    mark("stream:raster-start");
                    let fb = raster_scene(&fctx, field_w, field_h, &sc, &mut chrome_cache);
                    mark("stream:raster-end");
                    upload_field(&queue, &field_tex, field_w, field_h, &fb);
                    let sb = scene_sdf(&sctx, sdf_w, sdf_h, &sc, SDF_MAXDIST);
                    mark("stream:sdf-end");
                    upload_field(&queue, &sdf_tex, sdf_w, sdf_h, &sb);
                    scene_seen = sc.gen;
                    mark("stream:scene");
                }
            }
            if !scene_drawn && scene_seen > 0.0 && it > 1.4 {
                scene_drawn = true;
                js_sys::Reflect::set(&win, &"__SCENE_DRAWN".into(), &scene_seen.into()).ok();
            }
            let on = if scene_seen > 0.0 { name_op } else { 0.0 };
            phrase_op = on;
            phrase_w = on;
            phrase_z = 1.0;
            phrase_cy = 0.5;
        } else if it < INTRO_DUR {
            // hold the cycle frozen; the first phrase fades in last
            phase = 0;
            phase_start = now;
            let pop = ss(1.3, INTRO_DUR, it);
            phrase_op = pop;
            phrase_w = pop;
            phrase_z = 1.0;
        } else {
        let exit_dur = 600.0;
        let enter_dur = 600.0;
        let hold_dur = (PHRASE_SECONDS * 1000.0 - exit_dur - enter_dur).max(300.0);
        // a new phrase list (route change): go to its first phrase now
        let gen_now = phrases_gen();
        if gen_now != seen_gen {
            seen_gen = gen_now;
            let n = current_action_phrases(&baked_phrases).len().max(1);
            phrase_idx = n - 1; // the next swap lands on phrase 0
            if phase != 1 {
                phase = 1;
                phase_start = now;
            }
        }
        let el = now - phase_start;
        let phrase_tt: f64;
        if phase == 0 {
            phrase_tt = 0.0;
            // pre-warm: bake the NEXT phrase's SDF during the quiet hold so the
            // swap itself does zero work (cheap no-op once cached)
            let phrases = current_action_phrases(&baked_phrases);
            // bound the cache to the live phrase set (live editing won't leak)
            sdf_cache.retain(|k, _| phrases.iter().any(|p| p == k));
            let nxt = phrases[(phrase_idx + 1) % phrases.len()].clone();
            if !sdf_cache.contains_key(&nxt) {
                sdf_cache.insert(nxt.clone(), bake_sdf(&sctx, sdf_w, sdf_h, css_w, &nxt, SDF_MAXDIST));
            }
            if el >= hold_dur {
                phase = 1;
                phase_start = now;
            }
        } else if phase == 1 {
            let e = (el / exit_dur).min(1.0);
            phrase_tt = e * e * (3.0 - 2.0 * e);
            if el >= exit_dur {
                let phrases = current_action_phrases(&baked_phrases);
                phrase_idx = (phrase_idx + 1) % phrases.len();
                let (pe, cy) = phrase_layout(&fctx, field_w, field_h, css_w, &phrases[phrase_idx]);
                let (pb, ps) = raster_layer(&fctx, field_w, field_h, &pe);
                upload_field(
                    &queue,
                    &field_tex,
                    field_w,
                    field_h,
                    &pack_rgba(&name_blur, &name_sharp, &pb, &ps),
                );
                // wake SDF for the new phrase: cache hit (pre-warmed) → upload only,
                // else bake it now (a couple ms, hidden in this transition), then HARD
                // FAIL is impossible here since we just bake it.
                let pkey = phrases[phrase_idx].clone();
                if !sdf_cache.contains_key(&pkey) {
                    sdf_cache.insert(pkey.clone(), bake_sdf(&sctx, sdf_w, sdf_h, css_w, &pkey, SDF_MAXDIST));
                }
                upload_field(&queue, &sdf_tex, sdf_w, sdf_h, sdf_cache.get(&pkey).unwrap());
                phrase_cy = cy as f32;
                phase = 2;
                phase_start = now;
            }
        } else {
            let e = (el / enter_dur).min(1.0);
            phrase_tt = 1.0 - e * e * (3.0 - 2.0 * e);
            if el >= enter_dur {
                phase = 0;
                phase_start = now;
            }
        }
        phrase_op = (1.0 - phrase_tt) as f32;
        phrase_w = phrase_op; // physics weight tracks visibility
        phrase_z = (1.0 - 0.16 * phrase_tt) as f32;
        }


        // ── dragging + panel/section nav PARKED (see parked/drag-nav/) ──
        // The field is now driven only by the mouse/touch perturbation + the press
        // wake. drag_r's .2 is just "a finger/mouse is down" now (no offset).
        let (_pdu, _pdv, dragging) = drag_r.get();
        let pz_target = if dragging > 0.5 { 0.93f32 } else { 1.0f32 };
        let pz_rate = if dragging > 0.5 { 14.0f32 } else { 3.0f32 };
        press_z += (pz_target - press_z) * (1.0 - (-pz_rate * dt).exp());

        let (mx, my, mact) = mouse_r.get();
        let params = Params {
            res: [width as f32, height as f32],
            mouse: [mx, my],
            time: ((now - t0) / 1000.0) as f32,
            dt,
            count: particle_count,
            stream: dial("stream", bk("stream", 0.28)),
            push: 2.5,
            // MOUSE/TOUCH PERTURBATION — now the primary interaction, boosted. No longer
            // gated off while pressed; a press just ADDS the wake on top.
            mousef: dial("perturb", bk("perturb", 1.1)) * mact,
            dpr: dpr as f32,
            rot_speed: dial("rot_speed", bk("rot_speed", 0.27)),
            rot_depth: dial("rot_depth", bk("rot_depth", 3.2)),
            turb: dial("turb", bk("turb", 0.6)),
            eddy: dial("eddy", bk("eddy", 0.7)),
            sparkg: dial("spark", bk("spark", 1.05)),
            bg_freq: dial("bg_freq", bk("bg_freq", 2.6)),
            text_sat: dial("text_sat", bk("text_sat", 0.72)),
            bg_speed: dial("bg_speed", bk("bg_speed", 2.5)),
            mobile: if particle_count < PARTICLES { 1.0 } else { 0.0 },
            phrase_w,
            phrase_op,
            phrase_z,
            phrase_cy,
            bg_fade,
            part_fade,
            name_op: if smode { if scene_seen > 0.0 { name_op * s_op } else { 0.0 } } else { name_op },
            intro_glow,
            // nav offsets NEUTRALIZED (parked): text fixed, menu off-screen, no plow/push
            text_du: 0.0,
            // the site scene's page slide (content.js): the page layer moves
            // through the stream, and plows it while it does
            text_dv: s_dv,
            text_vx: 0.0,
            text_vy: s_vy,
            menu_du: 5.0,
            menu_dv: 5.0,
            pad0: 9.0,
            pad1: 9.0,
            wake: dial("wake", 1.0),
            porosity: dial("porosity", 0.55),
            pressed: if dragging > 0.5 { 1.0 } else { 0.0 },
            wake_width: dial("wake_width", 0.09),
            press_z,
            menu_vx: 0.0,
            menu_vy: 0.0,
            bump: 0.0,
        };
        queue.write_buffer(&param_buf, 0, bytemuck::bytes_of(&params));
        // SECTION TITLE parked (see parked/drag-nav/). Written with vis=0 so every
        // shader that reads titlex gates off (titlex.w > 0.02) and skips it.
        {
            let title_x: [f32; 4] = [1.0, 0.5, 0.5, 0.0];
            queue.write_buffer(&title_buf, 0, bytemuck::bytes_of(&title_x));
        }

        let frame = match surface.get_current_texture() {
            wgpu::CurrentSurfaceTexture::Success(t)
            | wgpu::CurrentSurfaceTexture::Suboptimal(t) => Some(t),
            _ => {
                surface.configure(&device, &config);
                None
            }
        };
        if let Some(frame) = frame {
            let view = frame.texture.create_view(&wgpu::TextureViewDescriptor::default());
            let mut enc =
                device.create_command_encoder(&wgpu::CommandEncoderDescriptor { label: None });
            {
                let mut cp = enc.begin_compute_pass(&wgpu::ComputePassDescriptor {
                    label: None,
                    timestamp_writes: None,
                });
                cp.set_pipeline(&sim_pipeline);
                cp.set_bind_group(0, &common_bg, &[]);
                cp.set_bind_group(1, &parts_bg, &[]);
                cp.set_bind_group(2, &title_bg, &[]);
                cp.dispatch_workgroups(groups, 1, 1);
            }
            fn pass<'a>(
                enc: &'a mut wgpu::CommandEncoder,
                target: &wgpu::TextureView,
            ) -> wgpu::RenderPass<'a> {
                enc.begin_render_pass(&wgpu::RenderPassDescriptor {
                    label: None,
                    color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                        view: target,
                        depth_slice: None,
                        resolve_target: None,
                        ops: wgpu::Operations {
                            load: wgpu::LoadOp::Clear(wgpu::Color::BLACK),
                            store: wgpu::StoreOp::Store,
                        },
                    })],
                    depth_stencil_attachment: None,
                    timestamp_writes: None,
                    occlusion_query_set: None,
                    multiview_mask: None,
                })
            }
            {
                // scene: bg + sharp text, then additive particles, in HDR
                let mut rp = pass(&mut enc, &scene_view);
                rp.set_bind_group(0, &common_bg, &[]);
                rp.set_pipeline(&bg_pipeline);
                rp.draw(0..3, 0..1);
                rp.set_pipeline(&p_pipeline);
                rp.set_vertex_buffer(0, particle_buf.slice(..));
                rp.draw(0..6, 0..particle_count);
            }
            {
                // bloom: bright-extract + horizontal blur into half-res A
                let mut rp = pass(&mut enc, &bloom_a_view);
                rp.set_bind_group(0, &bright_bg, &[]);
                rp.set_pipeline(&bright_pipeline);
                rp.draw(0..3, 0..1);
            }
            {
                // bloom: vertical blur A → B
                let mut rp = pass(&mut enc, &bloom_b_view);
                rp.set_bind_group(0, &blurv_bg, &[]);
                rp.set_pipeline(&blurv_pipeline);
                rp.draw(0..3, 0..1);
            }
            {
                // composite: scene + bloom, tonemapped, to the swapchain
                let mut rp = pass(&mut enc, &view);
                rp.set_bind_group(0, &comp_bg, &[]);
                rp.set_bind_group(1, &title_bg, &[]);
                rp.set_pipeline(&comp_pipeline);
                rp.draw(0..3, 0..1);
            }
            queue.submit([enc.finish()]);
            frame.present();
            if !presented {
                presented = true;
                mark("stream:first-frame");
                site_call("ready", &[]);
            }
        }

        win.request_animation_frame(f.borrow().as_ref().unwrap().as_ref().unchecked_ref())
            .unwrap();
    }) as Box<dyn FnMut()>));

    set_status("starting…");
    window
        .request_animation_frame(g.borrow().as_ref().unwrap().as_ref().unchecked_ref())
        .unwrap();
}
