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

const LINE1: &str = "BEN PRIDDY";
const PHRASES: [&str; 14] = [
    "BUILDS TECHNOLOGY",
    "CONSULTS ON TECHNOLOGY",
    "GUIDES CREATIVE",
    "TECHNOLOGIZES CREATIVE",
    "CREATIVIZES TECHNOLOGY",
    "CREATIVITIZES AI",
    "AI-IFIES PRACTICES",
    "PRACTICES AI",
    "TALKS TO AUDIENCES",
    "AUDIENCES TO TALKS",
    "PRACTICES CRAFT",
    "CRAFTS SYSTEMS",
    "SYSTEMIZES WONDER",
    "WONDERS, THEN BUILDS",
];
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
};
@group(0) @binding(0) var<uniform> P: Params;
@group(0) @binding(1) var field: texture_2d<f32>;
@group(0) @binding(2) var fsamp: sampler;
@group(1) @binding(0) var<storage, read_write> parts: array<Particle>;

fn pcg(v: u32) -> u32 {
  var s = v * 747796405u + 2891336453u;
  s = ((s >> ((s >> 28u) + 4u)) ^ s) * 277803737u;
  return (s >> 22u) ^ s;
}
fn rand01(v: u32) -> f32 { return f32(pcg(v)) / 4294967295.0; }

fn fieldAt(p: vec2<f32>) -> f32 {
  let uv = vec2<f32>(p.x * 0.5 + 0.5, 0.5 - p.y * 0.5);
  return textureSampleLevel(field, fsamp, uv, 0.0).r;
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
    let e = 0.012;
    let gx = fieldAt(pt.pos + vec2<f32>(e, 0.0)) - fieldAt(pt.pos - vec2<f32>(e, 0.0));
    let gy = fieldAt(pt.pos + vec2<f32>(0.0, e)) - fieldAt(pt.pos - vec2<f32>(0.0, e));
    let g = vec2<f32>(gx, gy);
    let gl = length(g);
    if (gl > 1e-5) {
      let n = g / gl;
      v -= n * P.push * (f * f * 4.0 + f * 0.6) * P.dt;
      let into = dot(v, n);
      if (into > 0.0) { v -= n * into * min(8.0 * f * P.dt, 0.9); }
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

  // speed cap
  let sp = length(v);
  if (sp > 1.4) { v *= 1.4 / sp; }

  var pos = pt.pos + v * P.dt;

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
  // can never overlap the letter edges.
  let fr = textureSampleLevel(field, fsamp, uv, 0.0);
  let shadow = fr.r * (1.0 - fr.g);
  col *= 1.0 - 0.55 * shadow;

  return vec4<f32>(col, 1.0);
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
            * (1.0 - P.mobile * 0.5);
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
  o.col = col * lum * 0.10;
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
};
@group(0) @binding(4) var<uniform> P: Params;

// the TEXT's own relief — sampled in continuous screen space so one surface
// spans the whole text block; rendered here, ABOVE scene + bloom
fn textHeight(p: vec2<f32>, t: f32) -> f32 {
  return sin(p.x * 2.2 - t * 0.9) * 0.60
       + cos(p.y * 2.8 + t * 0.7) * 0.60
       + sin((p.x - p.y) * 4.6 + t * 1.1) * 0.35
       + cos(p.x * 7.0 + p.y * 5.0 - t * 0.5) * 0.18;
}

fn aces(x: vec3<f32>) -> vec3<f32> {
  return clamp((x * (2.51 * x + 0.03)) / (x * (2.43 * x + 0.59) + 0.14),
               vec3<f32>(0.0), vec3<f32>(1.0));
}

@fragment
fn fs_comp(in: VOut) -> @location(0) vec4<f32> {
  let scene = textureSampleLevel(src, samp, in.uv, 0.0).rgb;
  let glow = textureSampleLevel(bloom, samp, in.uv, 0.0).rgb;
  // full, untouched bloom everywhere
  var c = aces((scene + glow * 1.25) * 0.92);

  // the type, rendered LAST, above everything: one alpha (crisp) both draws
  // the glyph and occludes whatever glow lies beneath it
  let crisp = smoothstep(0.42, 0.55, textureSampleLevel(fieldtex, samp, in.uv, 0.0).g);
  if (crisp > 0.001) {
    let aspect = P.res.x / P.res.y;
    let tt = P.time * 0.45;
    let tp = vec2<f32>((in.uv.x - 0.5) * aspect, in.uv.y - 0.5) * 2.4
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
    tcol = tcol * 1.18;
    c = mix(c, aces(tcol * 0.92), crisp);
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

fn set_status(text: &str) {
    if let Some(el) = web_sys::window()
        .and_then(|w| w.document())
        .and_then(|d| d.get_element_by_id("fps"))
    {
        el.set_text_content(Some(text));
    }
}

// Rasterize LINE1 + the current phrase into the two-channel field:
//   R — blurred coverage (deflection physics + soft drop shadow)
//   G — sharp coverage (crisp white type, drawn with no blur)
fn raster_field(
    ctx: &web_sys::CanvasRenderingContext2d,
    w: u32,
    h: u32,
    line2: &str,
    css_w: f64,
) -> Vec<u8> {
    let (wf, hf) = (w as f64, h as f64);
    let phone = hf > wf * 1.6; // aspect < ~0.62: phones
    let portrait = hf > wf * 0.85; // tablets upright

    // desktop font cap: the field maps to the full viewport, so a max SCREEN
    // size converts to field units via wf/css_w. ~96px/52px CSS keeps large
    // monitors close to the tablet look instead of billboard type.
    let f1 = (wf * 0.118).min(wf * 96.0 / css_w.max(1.0));
    let f2 = (wf * 0.064).min(wf * 52.0 / css_w.max(1.0));

    // layout: a list of (text, font-px, y) entries
    let mut entries: Vec<(String, f64, f64)> = Vec::new();
    if phone {
        // stack EVERY word — the name too — as a centered column
        let f1p = wf * 0.185;
        let f2p = wf * 0.095;
        let gap1 = f1p * 1.08;
        let gap2 = f2p * 1.3;
        let name_words: Vec<&str> = LINE1.split(' ').collect();
        let phrase_words: Vec<&str> = line2.split(' ').collect();
        let block = gap1 * name_words.len() as f64
            + gap2 * phrase_words.len() as f64
            + f2p * 0.6; // breathing room between name and phrase
        let mut y = hf * 0.5 - block / 2.0 + gap1 * 0.5;
        for wd in &name_words {
            entries.push((wd.to_string(), f1p, y));
            y += gap1;
        }
        y += f2p * 0.6;
        for wd in &phrase_words {
            entries.push((wd.to_string(), f2p, y));
            y += gap2;
        }
    } else if portrait {
        let words: Vec<&str> = line2.split(' ').collect();
        if words.len() >= 2 {
            // balanced split: minimize the length difference of the halves
            let mut best = 1usize;
            let mut bestdiff = usize::MAX;
            for k in 1..words.len() {
                let a = words[..k].join(" ").len();
                let b = words[k..].join(" ").len();
                let d = a.abs_diff(b);
                if d < bestdiff {
                    bestdiff = d;
                    best = k;
                }
            }
            let la = words[..best].join(" ");
            let lb = words[best..].join(" ");
            let f2s = wf * 0.088;
            entries.push((LINE1.to_string(), f1, hf * 0.5 - f2s * 1.55));
            entries.push((la, f2s, hf * 0.5 + f2s * 0.45));
            entries.push((lb, f2s, hf * 0.5 + f2s * 1.75));
        } else {
            entries.push((LINE1.to_string(), f1, hf * 0.455));
            entries.push((line2.to_string(), wf * 0.072, hf * 0.545));
        }
    } else {
        entries.push((LINE1.to_string(), f1, hf * 0.40));
        entries.push((line2.to_string(), f2, hf * 0.625));
    }

    let draw_lines = |ctx: &web_sys::CanvasRenderingContext2d| {
        for (text, px, y) in &entries {
            ctx.set_font(&format!(
                "900 {:.0}px -apple-system, system-ui, sans-serif",
                px
            ));
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

    // pass 1: blurred physics field
    clear(ctx);
    ctx.set_filter("blur(9px)");
    draw_lines(ctx);
    ctx.set_filter("blur(2px)");
    draw_lines(ctx);
    ctx.set_filter("none");
    let img = ctx.get_image_data(0.0, 0.0, wf, hf).unwrap();
    let blur_data = img.data();

    // pass 2: sharp text mask
    clear(ctx);
    draw_lines(ctx);
    let img2 = ctx.get_image_data(0.0, 0.0, wf, hf).unwrap();
    let sharp_data = img2.data();

    let n = (w * h) as usize;
    let mut out = Vec::with_capacity(n * 2);
    for i in 0..n {
        out.push(blur_data[i * 4]); // R: blurred
        out.push(sharp_data[i * 4]); // G: sharp
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
    let mouse = Rc::new(Cell::new((0.0f32, 0.0f32, 0.0f32))); // x, y, active
    {
        let m = mouse.clone();
        let (w, h) = (css_w as f32, css_h as f32);
        let cb = Closure::<dyn FnMut(web_sys::MouseEvent)>::new(move |e: web_sys::MouseEvent| {
            let x = (e.client_x() as f32 / w) * 2.0 - 1.0;
            let y = -((e.client_y() as f32 / h) * 2.0 - 1.0);
            m.set((x, y, 1.0));
        });
        window
            .add_event_listener_with_callback("mousemove", cb.as_ref().unchecked_ref())
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
    for _ in 0..particle_count {
        init.push(rnd(&mut rng) * 2.2 - 1.1);
        init.push(rnd(&mut rng) * 2.0 - 1.0);
        init.push(0.25 + rnd(&mut rng) * 0.2);
        init.push(0.0);
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

    // two-channel field: R = blurred physics, G = sharp type
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
        format: wgpu::TextureFormat::Rg8Unorm,
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
                bytes_per_row: Some(fw * 2),
                rows_per_image: Some(fh),
            },
            wgpu::Extent3d {
                width: fw,
                height: fh,
                depth_or_array_layers: 1,
            },
        );
    }
    upload_field(
        &queue,
        &field_tex,
        field_w,
        field_h,
        &raster_field(&fctx, field_w, field_h, PHRASES[0], css_w),
    );

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
        ],
    });

    let common_bg = device.create_bind_group(&wgpu::BindGroupDescriptor {
        label: None,
        layout: &common_bgl,
        entries: &[
            wgpu::BindGroupEntry { binding: 0, resource: param_buf.as_entire_binding() },
            wgpu::BindGroupEntry { binding: 1, resource: wgpu::BindingResource::TextureView(&field_view) },
            wgpu::BindGroupEntry { binding: 2, resource: wgpu::BindingResource::Sampler(&lin_samp) },
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
    let mut phrase_idx: usize = 0;
    let mut phrase_t = t0;

    let f = Rc::new(RefCell::new(None::<Closure<dyn FnMut()>>));
    let g = f.clone();
    let win = window.clone();
    let mouse_r = mouse.clone();
    *g.borrow_mut() = Some(Closure::wrap(Box::new(move || {
        let now = perf.now();
        let dt_ms = now - last;
        last = now;
        let dt = (dt_ms / 1000.0).min(0.033) as f32;
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

        // cycle the phrase: new rocks drop into the stream
        if now - phrase_t > PHRASE_SECONDS * 1000.0 {
            phrase_t = now;
            phrase_idx = (phrase_idx + 1) % PHRASES.len();
            upload_field(
                &queue,
                &field_tex,
                field_w,
                field_h,
                &raster_field(&fctx, field_w, field_h, PHRASES[phrase_idx], css_w),
            );
        }

        let (mx, my, mact) = mouse_r.get();
        let params = Params {
            res: [width as f32, height as f32],
            mouse: [mx, my],
            time: ((now - t0) / 1000.0) as f32,
            dt,
            count: particle_count,
            stream: dial("stream", 0.28),
            push: 2.5,
            mousef: 0.5 * mact,
            dpr: dpr as f32,
            rot_speed: dial("rot_speed", 0.27),
            rot_depth: dial("rot_depth", 3.2),
            turb: dial("turb", 0.6),
            eddy: dial("eddy", 0.7),
            sparkg: dial("spark", 1.05),
            bg_freq: dial("bg_freq", 2.6),
            text_sat: dial("text_sat", 0.72),
            bg_speed: dial("bg_speed", 2.5),
            mobile: if particle_count < PARTICLES { 1.0 } else { 0.0 },
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
