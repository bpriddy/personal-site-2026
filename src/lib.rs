use std::cell::{Cell, RefCell};
use std::rc::Rc;
use wasm_bindgen::prelude::*;
use wasm_bindgen::JsCast;

mod interaction_intent;
use interaction_intent::drag_intent::DragIntent;

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

const LINE1: &str = "BEN PRIDDY";
// The phrase list lives in phrases.json (baked at build time, editable live via
// the hidden phrase panel → window.__PHRASES). PHRASE_SECONDS sets the cycle.
const PHRASE_SECONDS: f64 = 4.5;

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
    pz2: f32,
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
  press_z: f32, menu_vx: f32, menu_vy: f32, pz2: f32,
};
@group(0) @binding(0) var<uniform> P: Params;
@group(0) @binding(1) var field: texture_2d<f32>;
@group(0) @binding(2) var fsamp: sampler;
@group(0) @binding(3) var menu: texture_2d<f32>;
@group(0) @binding(4) var sdftex: texture_2d<f32>; // wake distance field (RG=dir, B=dist)
@group(0) @binding(5) var menusdf: texture_2d<f32>; // off-screen panel wake field
@group(1) @binding(0) var<storage, read_write> parts: array<Particle>;

fn pcg(v: u32) -> u32 {
  var s = v * 747796405u + 2891336453u;
  s = ((s >> ((s >> 28u) + 4u)) ^ s) * 277803737u;
  return (s >> 22u) ^ s;
}
fn rand01(v: u32) -> f32 { return f32(pcg(v)) / 4294967295.0; }

fn fieldAt(p: vec2<f32>) -> f32 {
  let uv = vec2<f32>(p.x * 0.5 + 0.5 - P.text_du, 0.5 - p.y * 0.5 - P.text_dv);
  let s = textureSampleLevel(field, fsamp, uv, 0.0);
  let muv = (vec2<f32>(p.x * 0.5 + 0.5, 0.5 - p.y * 0.5) + vec2<f32>(1.0, 1.0) - vec2<f32>(P.menu_du, P.menu_dv)) / 3.0;
  // only the single active panel cell (pad0,pad1) contributes - never its neighbours
  let inCell = abs(muv.x - P.pad0) < 0.1667 && abs(muv.y - P.pad1) < 0.1667;
  let mr = select(0.0, textureSampleLevel(menu, fsamp, muv, 0.0).r, inCell);
  // name (R) permanent; phrase (B) fades with z; panel (mr) pans in opposite the drag
  return max(max(s.r * P.name_op, s.b * P.phrase_w), mr);
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

  // gentle mouse drag — a finger through the water
  let md = pt.pos - P.mouse;
  let mr2 = dot(md, md);
  if (P.mousef > 0.001 && mr2 < 0.09) {
    v += (md / max(sqrt(mr2), 0.02)) * P.mousef * exp(-mr2 * 26.0) * P.dt;
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
  // Engaged while pressed OR still moving, so the throw keeps plowing to the end.
  if (engaged) {
    let aspect = P.res.x / P.res.y;
    let scuv = vec2<f32>(pos.x * 0.5 + 0.5, 0.5 - pos.y * 0.5);
    let nsdf = textureSampleLevel(sdftex, fsamp, scuv - vec2<f32>(P.text_du, P.text_dv), 0.0);
    let nd = nsdf.b * 0.35;
    // RENORMALIZE: linear filtering across the direction field's discontinuities
    // (glyph gaps / interior-zero plateau) returns a sub-unit vector; without this
    // the snap under-displaces and leaves particles inside the wake (residual skim).
    let nraw = nsdf.rg * 2.0 - vec2<f32>(1.0, 1.0);
    let nlen = length(nraw);
    let ns = select(vec2<f32>(0.0, 0.0), nraw / nlen, nlen > 1e-3); // isotropic-screen outward
    let nndc = vec2<f32>(ns.x, -ns.y * aspect); // NDC outward (aspect-correct)
    let nclos = dot(vec2<f32>(P.text_vx, P.text_vy), nndc);
    if (nd < P.wake_width && nclos > 0.0) {
      let isp = vec2<f32>(scuv.x, scuv.y / aspect) + ns * (P.wake_width - nd);
      let su = vec2<f32>(isp.x, isp.y * aspect);
      pos = vec2<f32>(su.x * 2.0 - 1.0, 1.0 - su.y * 2.0);
      let vn = normalize(nndc);
      let vin = dot(v, vn);
      if (vin < 0.0) { v -= vn * vin; } // drop inward velocity so it won't fight the edge
    }
    let scuv2 = vec2<f32>(pos.x * 0.5 + 0.5, 0.5 - pos.y * 0.5);
    let mu = (scuv2 + vec2<f32>(1.0, 1.0) - vec2<f32>(P.menu_du, P.menu_dv)) / 3.0;
    if (abs(mu.x - P.pad0) < 0.1667 && abs(mu.y - P.pad1) < 0.1667) {
      let msd = textureSampleLevel(menusdf, fsamp, mu, 0.0);
      let md2 = msd.b * 0.35;
      let mraw = msd.rg * 2.0 - vec2<f32>(1.0, 1.0);
      let mlen = length(mraw);
      let ms = select(vec2<f32>(0.0, 0.0), mraw / mlen, mlen > 1e-3);
      let mndc = vec2<f32>(ms.x, -ms.y * aspect);
      let mclos = dot(vec2<f32>(P.menu_vx, P.menu_vy), mndc);
      if (md2 < P.wake_width && mclos > 0.0) {
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
  press_z: f32, menu_vx: f32, menu_vy: f32, pz2: f32,
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
  let p0 = vec2<f32>((uv.x - 0.5) * aspect, uv.y - 0.5) * 3.0 * P.bg_freq;
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
  let name_sh = fr.r * (1.0 - fr.g) * P.name_op;
  let phrase_sh = fr.b * P.phrase_w * (1.0 - fr.a);
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
  press_z: f32, menu_vx: f32, menu_vy: f32, pz2: f32,
};
@group(0) @binding(4) var<uniform> P: Params;
@group(0) @binding(5) var menutex: texture_2d<f32>;

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
  let puv = pivot + (zuv - vec2<f32>(P.text_du, P.text_dv) - pivot) / max(P.phrase_z, 0.01);
  let phrase_c = smoothstep(0.42, 0.55,
    textureSampleLevel(fieldtex, samp, puv, 0.0).a) * P.phrase_op;

  let m_uv = (zuv + vec2<f32>(1.0, 1.0) - vec2<f32>(P.menu_du, P.menu_dv)) / 3.0;
  let m_in = abs(m_uv.x - P.pad0) < 0.1667 && abs(m_uv.y - P.pad1) < 0.1667;
  let menu_c = select(0.0, smoothstep(0.42, 0.55, textureSampleLevel(menutex, samp, m_uv, 0.0).g), m_in);
  let cover = max(max(name_c, phrase_c), menu_c);
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

// the live phrase list from window.__PHRASES (editor panel); falls back to the
// baked phrases.json when unset or empty so the viz never breaks
fn current_phrases(fallback: &[String]) -> Vec<String> {
    let arr = web_sys::window()
        .and_then(|w| js_sys::Reflect::get(&w, &"__PHRASES".into()).ok())
        .and_then(|v| v.dyn_into::<js_sys::Array>().ok());
    if let Some(a) = arr {
        let mut out = Vec::new();
        for i in 0..a.length() {
            if let Some(t) = a.get(i).as_string() {
                let t = t.trim().to_string();
                if !t.is_empty() { out.push(t); }
            }
        }
        if !out.is_empty() { return out; }
    }
    fallback.to_vec()
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

// the NAME is anchored at a FIXED y per tier — it never moves as phrases change
fn name_layout(w: u32, h: u32, css_w: f64) -> Vec<(String, f64, f64)> {
    let (wf, hf) = (w as f64, h as f64);
    let (phone, portrait, f1, _f2) = tier_fonts(w, h, css_w);
    let mut e = Vec::new();
    if phone {
        let f1p = wf * 0.205;
        let gap1 = f1p * 1.08;
        let mut y = hf * 0.27;
        for wd in LINE1.split(' ') {
            e.push((wd.to_string(), f1p, y));
            y += gap1;
        }
    } else if portrait {
        e.push((LINE1.to_string(), f1, hf * 0.40));
    } else {
        e.push((LINE1.to_string(), f1, hf * 0.5 - f1 * 0.46));
    }
    e
}

// the PHRASE lays out below the anchored name; returns its lines + block center
// (uv.y), which the composite uses as the z-scale pivot
fn phrase_layout(w: u32, h: u32, css_w: f64, phrase: &str) -> (Vec<(String, f64, f64)>, f64) {
    let (wf, hf) = (w as f64, h as f64);
    let (phone, portrait, _f1, f2) = tier_fonts(w, h, css_w);
    let mut e = Vec::new();
    let cy;
    if phone {
        let f2p = wf * 0.108;
        let gap2 = f2p * 1.25;
        let top = hf * 0.58;
        let words: Vec<&str> = phrase.split(' ').collect();
        let mut y = top;
        for wd in &words {
            e.push((wd.to_string(), f2p, y));
            y += gap2;
        }
        cy = (top + gap2 * (words.len() as f64 - 1.0) * 0.5) / hf;
    } else if portrait {
        let words: Vec<&str> = phrase.split(' ').collect();
        let top = hf * 0.55;
        if words.len() >= 2 {
            let mut best = 1usize;
            let mut bestdiff = usize::MAX;
            for k in 1..words.len() {
                let d = words[..k].join(" ").len().abs_diff(words[k..].join(" ").len());
                if d < bestdiff {
                    bestdiff = d;
                    best = k;
                }
            }
            let f2s = wf * 0.088;
            let gap = f2s * 1.28;
            e.push((words[..best].join(" "), f2s, top));
            e.push((words[best..].join(" "), f2s, top + gap));
            cy = (top + gap * 0.5) / hf;
        } else {
            let f2s = wf * 0.082;
            e.push((phrase.to_string(), f2s, top));
            cy = top / hf;
        }
    } else {
        let py = hf * 0.5 + f2 * 1.05;
        e.push((phrase.to_string(), f2, py));
        cy = py / hf;
    }
    (e, cy)
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
    entries.extend(phrase_layout(sw, sh, css_w, phrase).0);
    let (_, sharp) = raster_layer(ctx, sw, sh, &entries);
    coverage_to_sdf(&sharp, sw, sh, sw as f32, maxdist)
}

// wake SDF for the off-screen panel atlas (static, baked once). The atlas spans
// 3 screens across `w`, so one screen width = w/3 px.
fn bake_atlas_sdf(ctx: &web_sys::CanvasRenderingContext2d, w: u32, h: u32, maxdist: f32) -> Vec<u8> {
    let entries = menu_atlas_entries(w, h);
    let (_, sharp) = raster_atlas(ctx, w, h, &entries);
    coverage_to_sdf(&sharp, w, h, w as f32 / 3.0, maxdist)
}

// the 8 off-screen panel words at their 3x3 atlas cell centres, sized for a
// w x h canvas — shared by the live atlas raster and the wake-SDF bake
fn menu_atlas_entries(w: u32, h: u32) -> Vec<(&'static str, f64, f64, f64)> {
    let (cw, ch) = (w as f64, h as f64);
    // panel font (atlas is 3x screen). On the phone tier the name jumps to ~0.205,
    // so the panels scale up to match - capped at 0.05 so the longest word
    // (NORTHWEST) still fits inside a 1/3-width cell.
    let phone = ch > cw * 1.6;
    let pf = if phone { cw * 0.05 } else { cw * 0.033 };
    vec![
        ("NORTHWEST", pf, 0.1667 * cw, 0.1667 * ch),
        ("MENU", pf, 0.5 * cw, 0.1667 * ch),
        ("NORTHEAST", pf, 0.8333 * cw, 0.1667 * ch),
        ("WEST", pf, 0.1667 * cw, 0.5 * ch),
        ("EAST", pf, 0.8333 * cw, 0.5 * ch),
        ("SOUTHWEST", pf, 0.1667 * cw, 0.8333 * ch),
        ("SOUTH", pf, 0.5 * cw, 0.8333 * ch),
        ("SOUTHEAST", pf, 0.8333 * cw, 0.8333 * ch),
    ]
}

// interleave name(RG) + phrase(BA) coverage into one RGBA upload buffer
// rasterize text at arbitrary (cx, cy) into a (blur, sharp) coverage pair —
// used to bake the static off-screen panel atlas (one raster, all panels)
fn raster_atlas(
    ctx: &web_sys::CanvasRenderingContext2d,
    w: u32,
    h: u32,
    entries: &[(&str, f64, f64, f64)],
) -> (Vec<u8>, Vec<u8>) {
    let (wf, hf) = (w as f64, h as f64);
    let draw = |ctx: &web_sys::CanvasRenderingContext2d| {
        for (text, px, cx, cy) in entries {
            ctx.set_font(&format!("900 {:.0}px -apple-system, system-ui, sans-serif", px));
            ctx.fill_text(text, *cx, *cy).ok();
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
    // phrases.json: baked phrase list, pushed to the editor panel; runtime edits
    // (window.__PHRASES) override it. Parse to a Vec for the fallback.
    let baked_phrases: Vec<String> = {
        let v = js_sys::JSON::parse(include_str!("../phrases.json"))
            .unwrap_or(wasm_bindgen::JsValue::NULL);
        if let Ok(f) = js_sys::Reflect::get(&window, &"__initPhrases".into()) {
            if let Some(func) = f.dyn_ref::<js_sys::Function>() {
                func.call1(&wasm_bindgen::JsValue::NULL, &v).ok();
            }
        }
        let mut out = Vec::new();
        if let Ok(arr) = v.dyn_into::<js_sys::Array>() {
            for i in 0..arr.length() {
                if let Some(t) = arr.get(i).as_string() { out.push(t); }
            }
        }
        if out.is_empty() { out.push("BUILDS TECHNOLOGY".to_string()); }
        out
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
        let retry = Closure::<dyn FnMut()>::new(move || {
            if let Some(w) = web_sys::window() {
                w.location().reload().ok();
            }
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

    // a fully procedural page can simply reload on resize (debounced)
    {
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
    let fctx: web_sys::CanvasRenderingContext2d =
        fcanvas.get_context("2d").unwrap().unwrap().dyn_into().unwrap();

    // mouse → NDC, shared with the frame loop
    let mouse = Rc::new(Cell::new((0.0f32, 0.0f32, 0.0f32))); // x, y, active (perturbance)
    // text drag: (target_du, target_dv, dragging) in field-UV; anchor = grab uv
    // + offset-at-grab; offset_pub is the live offset the frame loop publishes
    // so a fresh grab continues from where the text currently is
    let drag = Rc::new(Cell::new((0.0f32, 0.0f32, 0.0f32)));
    let anchor = Rc::new(Cell::new((0.0f32, 0.0f32, 0.0f32, 0.0f32)));
    let offset_pub = Rc::new(Cell::new((0.0f32, 0.0f32)));

    // mouse move: drag the text if pressed, else perturb the stream
    {
        let m = mouse.clone();
        let dr = drag.clone();
        let an = anchor.clone();
        let (w, h) = (css_w as f32, css_h as f32);
        let cb = Closure::<dyn FnMut(web_sys::MouseEvent)>::new(move |e: web_sys::MouseEvent| {
            if dr.get().2 > 0.5 {
                let (pu, pv, bu, bv) = an.get();
                let du = bu + (e.client_x() as f32 / w - pu);
                let dv = bv + (e.client_y() as f32 / h - pv);
                dr.set((du, dv, 1.0));
            } else {
                let x = (e.client_x() as f32 / w) * 2.0 - 1.0;
                let y = -((e.client_y() as f32 / h) * 2.0 - 1.0);
                m.set((x, y, 1.0));
            }
        });
        window
            .add_event_listener_with_callback("mousemove", cb.as_ref().unchecked_ref())
            .unwrap();
        cb.forget();
    }
    {
        let dr = drag.clone();
        let an = anchor.clone();
        let op = offset_pub.clone();
        let (w, h) = (css_w as f32, css_h as f32);
        let cb = Closure::<dyn FnMut(web_sys::MouseEvent)>::new(move |e: web_sys::MouseEvent| {
            let (bu, bv) = op.get();
            an.set((e.client_x() as f32 / w, e.client_y() as f32 / h, bu, bv));
            dr.set((bu, bv, 1.0));
        });
        canvas
            .add_event_listener_with_callback("mousedown", cb.as_ref().unchecked_ref())
            .unwrap();
        cb.forget();
    }
    {
        let dr = drag.clone();
        let cb = Closure::<dyn FnMut(web_sys::MouseEvent)>::new(move |_e: web_sys::MouseEvent| {
            let (du, dv, _) = dr.get();
            dr.set((du, dv, 0.0));
        });
        window
            .add_event_listener_with_callback("mouseup", cb.as_ref().unchecked_ref())
            .unwrap();
        cb.forget();
    }
    {
        let m = mouse.clone();
        let cb = Closure::<dyn FnMut(web_sys::MouseEvent)>::new(move |_e: web_sys::MouseEvent| {
            let (x, y, _) = m.get();
            m.set((x, y, 0.0));
        });
        document
            .add_event_listener_with_callback("mouseleave", cb.as_ref().unchecked_ref())
            .unwrap();
        cb.forget();
    }
    // touch: press-drag throws the text (its moving obstacle stirs the field)
    {
        let dr = drag.clone();
        let an = anchor.clone();
        let op = offset_pub.clone();
        let (w, h) = (css_w as f32, css_h as f32);
        let cb = Closure::<dyn FnMut(web_sys::TouchEvent)>::new(move |e: web_sys::TouchEvent| {
            if let Some(t) = e.touches().get(0) {
                let (bu, bv) = op.get();
                an.set((t.client_x() as f32 / w, t.client_y() as f32 / h, bu, bv));
                dr.set((bu, bv, 1.0));
            }
        });
        canvas
            .add_event_listener_with_callback("touchstart", cb.as_ref().unchecked_ref())
            .unwrap();
        cb.forget();
    }
    {
        let dr = drag.clone();
        let an = anchor.clone();
        let (w, h) = (css_w as f32, css_h as f32);
        let cb = Closure::<dyn FnMut(web_sys::TouchEvent)>::new(move |e: web_sys::TouchEvent| {
            if dr.get().2 > 0.5 {
                if let Some(t) = e.touches().get(0) {
                    let (pu, pv, bu, bv) = an.get();
                    let du = bu + (t.client_x() as f32 / w - pu);
                    let dv = bv + (t.client_y() as f32 / h - pv);
                    dr.set((du, dv, 1.0));
                }
            }
        });
        window
            .add_event_listener_with_callback("touchmove", cb.as_ref().unchecked_ref())
            .unwrap();
        cb.forget();
    }
    {
        let dr = drag.clone();
        let cb = Closure::<dyn FnMut(web_sys::TouchEvent)>::new(move |_e: web_sys::TouchEvent| {
            let (du, dv, _) = dr.get();
            dr.set((du, dv, 0.0));
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
            return;
        }
    };
    let (device, queue) = adapter
        .request_device(&wgpu::DeviceDescriptor {
            label: None,
            required_features: wgpu::Features::empty(),
            required_limits: wgpu::Limits::default(),
            memory_hints: wgpu::MemoryHints::default(),
            experimental_features: wgpu::ExperimentalFeatures::default(),
            trace: wgpu::Trace::Off,
        })
        .await
        .expect("request_device failed");

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
    let first_phrase = current_phrases(&baked_phrases)
        .into_iter()
        .next()
        .unwrap_or_else(|| baked_phrases[0].clone());
    let (p_entries0, phrase_cy0) = phrase_layout(field_w, field_h, css_w, &first_phrase);
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
    let menu_entries = menu_atlas_entries(field_w, field_h);
    let (menu_blur, menu_sharp) = raster_atlas(&fctx, field_w, field_h, &menu_entries);
    let menu_zero = vec![0u8; menu_blur.len()];
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
        &pack_rgba(&menu_blur, &menu_sharp, &menu_zero, &menu_zero));

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
    let sctx: web_sys::CanvasRenderingContext2d =
        scanvas.get_context("2d").unwrap().unwrap().dyn_into().unwrap();
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
    upload_field(&queue, &menu_sdf_tex, sdf_w, sdf_h,
        &bake_atlas_sdf(&sctx, sdf_w, sdf_h, SDF_MAXDIST));

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

    let compute_pl = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
        label: None,
        bind_group_layouts: &[Some(&common_bgl), Some(&parts_bgl)],
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
        bind_group_layouts: &[Some(&comp_bgl)],
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
    let mut tx_off = (0.0f32, 0.0f32); // text drag offset (field-UV)
    let mut tx_vel = (0.0f32, 0.0f32);
    let mut last_dir = (0.0f32, 1.0f32); // last 8-snapped drag direction
    let mut snap_target = (0.0f32, 0.0f32);
    let mut committed = (0.0f32, 0.0f32); // resting state: name (origin) or a panel
    let mut was_dragging = false;
    let mut press_z = 1.0f32; // visible-text recede: dips on press, eases back on release
    let mut drag_intent = DragIntent::new();
    drag_intent.set_axis_mode(true); // lock to an axis → drag both ways (up & down, etc.)
    let mut phrase_idx: usize = 0;
    let mut phase: u8 = 0; // 0 hold, 1 exit (push back + fade), 2 enter (forward + fade in)
    let mut phase_start = t0;
    let mut phrase_cy = phrase_cy0 as f32;

    let f = Rc::new(RefCell::new(None::<Closure<dyn FnMut()>>));
    let g = f.clone();
    let win = window.clone();
    let mouse_r = mouse.clone();
    let drag_r = drag.clone();
    let offpub_r = offset_pub.clone();
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
        if it < INTRO_DUR {
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
        let el = now - phase_start;
        let phrase_tt: f64;
        if phase == 0 {
            phrase_tt = 0.0;
            // pre-warm: bake the NEXT phrase's SDF during the quiet hold so the
            // swap itself does zero work (cheap no-op once cached)
            let phrases = current_phrases(&baked_phrases);
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
                let phrases = current_phrases(&baked_phrases);
                phrase_idx = (phrase_idx + 1) % phrases.len();
                let (pe, cy) = phrase_layout(field_w, field_h, css_w, &phrases[phrase_idx]);
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

        // The drag slides along ONE AXIS (a line) between three states: name
        // centred (origin) and the two panels at +/- the axis. Axis mode lets the
        // gesture go BOTH ways - lock the y axis, then drag DOWN to bring in the
        // panel above (north) or UP to bring in the one below (south). The offset
        // is clamped to [-axis, +axis] so you can't over-pull past a panel; the
        // panel shown is the one on whichever side you drag toward.
        let (tdu, tdv, dragging) = drag_r.get();
        drag_intent.set_commit_threshold(dial("commit", 0.3)); // live FEEL dial
        let at_panel = committed.0.abs() + committed.1.abs() > 1e-4;
        if dragging > 0.5 {
            if !was_dragging {
                drag_intent.begin((tdu, tdv));
            }
            let target = drag_intent.update((tdu, tdv), dt);
            // the locked axis: pinned to the committed panel's axis when on one,
            // else the canonical axis the drag locked onto
            let axis = if at_panel {
                (last_dir.0.round(), last_dir.1.round())
            } else if let Some(d) = drag_intent.direction() {
                (d.0.round(), d.1.round())
            } else {
                (0.0, 0.0)
            };
            // clamp the offset onto the [-axis, +axis] line (both directions)
            let plen = (axis.0 * axis.0 + axis.1 * axis.1).sqrt();
            let constrained = if plen > 1e-5 {
                let ah = (axis.0 / plen, axis.1 / plen);
                let along = (target.0 * ah.0 + target.1 * ah.1).clamp(-plen, plen);
                (ah.0 * along, ah.1 * along)
            } else {
                committed // pre-lock: hold at rest until a direction reads
            };
            let ivx = (constrained.0 - tx_off.0) / dt.max(0.001);
            let ivy = (constrained.1 - tx_off.1) / dt.max(0.001);
            tx_vel = ((tx_vel.0 * 0.55 + ivx * 0.45).clamp(-10.0, 10.0),
                      (tx_vel.1 * 0.55 + ivy * 0.45).clamp(-10.0, 10.0));
            tx_off = constrained;
            if !at_panel {
                if let Some(d) = drag_intent.direction() { last_dir = d; }
            }
            was_dragging = true;
        } else {
            if was_dragging {
                drag_intent.end();
                // snap to the nearest of three detents along the axis: the panel on
                // whichever side we pulled toward (once past the threshold), or the
                // name. Hysteresis: leaving a panel takes the same threshold.
                let axis = (last_dir.0.round(), last_dir.1.round());
                let plen2 = axis.0 * axis.0 + axis.1 * axis.1;
                let f = if plen2 > 1e-5 { (tx_off.0 * axis.0 + tx_off.1 * axis.1) / plen2 } else { 0.0 };
                let thr = drag_intent.commit_threshold();
                let neg = (-axis.0, -axis.1);
                let cs = committed.0 * axis.0 + committed.1 * axis.1; // side we were resting on
                snap_target = if cs > 0.5 {
                    if f > 1.0 - thr { axis } else if f < -thr { neg } else { (0.0, 0.0) }
                } else if cs < -0.5 {
                    if f < -(1.0 - thr) { neg } else if f > thr { axis } else { (0.0, 0.0) }
                } else {
                    if f > thr { axis } else if f < -thr { neg } else { (0.0, 0.0) }
                };
                committed = snap_target;
                tx_vel = (0.0, 0.0); // clean settle - drop any clamp/flick velocity spike
                was_dragging = false;
            }
            // slightly over-damped spring so it never overshoots past the target
            let k = 130.0f32;
            let c = 24.0f32;
            tx_vel = (tx_vel.0 + (-k * (tx_off.0 - snap_target.0) - c * tx_vel.0) * dt,
                      tx_vel.1 + (-k * (tx_off.1 - snap_target.1) - c * tx_vel.1) * dt);
            tx_off = (tx_off.0 + tx_vel.0 * dt, tx_off.1 + tx_vel.1 * dt);
        }
        offpub_r.set(tx_off);
        // the panel atlas pans DIRECTLY with the drag (menu uniforms use tx_off, same
        // as the name) so it drags and throws with identical feel - no lerp lag.
        // on press the visible text recedes slightly in z to meet the wake growth,
        // then eases back to normal more slowly on release
        let pz_target = if dragging > 0.5 { 0.93f32 } else { 1.0f32 };
        let pz_rate = if dragging > 0.5 { 14.0f32 } else { 3.0f32 };
        press_z += (pz_target - press_z) * (1.0 - (-pz_rate * dt).exp());
        // active panel cell centre in atlas-uv, for the shader mask (only this cell
        // shows). The active panel is on whichever SIDE of the axis the offset is,
        // so dragging the other way reveals the opposite panel. centre = (1.5-cell)/3
        let axis_n = (last_dir.0.round(), last_dir.1.round());
        let alen = (axis_n.0 * axis_n.0 + axis_n.1 * axis_n.1).sqrt();
        let along_now = if alen > 1e-5 { (tx_off.0 * axis_n.0 + tx_off.1 * axis_n.1) / alen } else { 0.0 };
        let side = if along_now >= 0.0 { axis_n } else { (-axis_n.0, -axis_n.1) };
        let menu_cell = ((1.5 - side.0) / 3.0, (1.5 - side.1) / 3.0);
        // the name leads further off-screen than the panel pans in, so the two
        // don't crowd each other on the way out or back
        let name_lead = dial("name_lead", 1.5);

        let (mx, my, mact) = mouse_r.get();
        let params = Params {
            res: [width as f32, height as f32],
            mouse: [mx, my],
            time: ((now - t0) / 1000.0) as f32,
            dt,
            count: particle_count,
            stream: dial("stream", bk("stream", 0.28)),
            push: 2.5,
            mousef: if dragging > 0.5 { 0.0 } else { dial("perturb", bk("perturb", 0.5)) * mact },
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
            name_op,
            intro_glow,
            text_du: tx_off.0 * name_lead,
            text_dv: tx_off.1 * name_lead,
            text_vx: tx_vel.0 * 2.0 * name_lead,
            text_vy: tx_vel.1 * -2.0 * name_lead,
            menu_du: tx_off.0,
            menu_dv: tx_off.1,
            pad0: menu_cell.0,
            pad1: menu_cell.1,
            wake: dial("wake", 1.0),
            porosity: dial("porosity", 0.55),
            pressed: if dragging > 0.5 { 1.0 } else { 0.0 },
            wake_width: dial("wake_width", 0.09),
            press_z,
            menu_vx: tx_vel.0 * 2.0,
            menu_vy: tx_vel.1 * -2.0,
            pz2: 0.0,
        };
        queue.write_buffer(&param_buf, 0, bytemuck::bytes_of(&params));

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
                rp.set_pipeline(&comp_pipeline);
                rp.draw(0..3, 0..1);
            }
            queue.submit([enc.finish()]);
            frame.present();
        }

        win.request_animation_frame(f.borrow().as_ref().unwrap().as_ref().unchecked_ref())
            .unwrap();
    }) as Box<dyn FnMut()>));

    set_status("starting…");
    window
        .request_animation_frame(g.borrow().as_ref().unwrap().as_ref().unchecked_ref())
        .unwrap();
}
