use std::cell::{Cell, RefCell};
use std::rc::Rc;
use wasm_bindgen::prelude::*;
use wasm_bindgen::JsCast;

// ─────────────────────────────────────────────────────────────────────────────
// "Words as rocks in a stream."
//
// A dense GPU-compute particle stream flows left→right across the screen. The
// two text lines ("BEN PRIDDY" + a cycling phrase) are NOT attractors — they're
// obstacles: the text is rasterized (blurred) into a scalar field texture, and
// the compute shader deflects particles along the field's gradient, so the
// stream parts around the letterforms like water around rocks. Particles
// naturally accumulate and stall at the upstream faces, where they sparkle —
// noon sun on water. Colors come from the warm half of a normal-map palette
// (flow direction → RG of a normal encoding, blue suppressed). The mouse drags
// gently through the stream. Background: a dim normal-mapped riverbed that the
// glyph field subtly embosses.
// ─────────────────────────────────────────────────────────────────────────────

const LINE1: &str = "BEN PRIDDY";
const PHRASES: [&str; 12] = [
    "BUILDS TECHNOLOGY",
    "CONSULTS ON TECHNOLOGY",
    "GUIDES CREATIVE",
    "TECHNOLOGIZES CREATIVE",
    "CREATIVIZES TECHNOLOGY",
    "CREATIVITIZES AI",
    "AI-IFIES PRACTICES",
    "PRACTICES AI",
    "PRACTICES CRAFT",
    "CRAFTS SYSTEMS",
    "SYSTEMIZES WONDER",
    "WONDERS, THEN BUILDS",
];
const PHRASE_SECONDS: f64 = 4.5;

const PARTICLES: u32 = 380_000;
const WG: u32 = 64;
const FIELD_W: u32 = 1024;

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
    _pad: f32,
}

// Compute: integrate particles against the obstacle field.
const SIM_SHADER: &str = r#"
struct Particle { pos: vec2<f32>, vel: vec2<f32> };
struct Params {
  res: vec2<f32>, mouse: vec2<f32>,
  time: f32, dt: f32, count: u32, stream: f32,
  push: f32, mousef: f32, dpr: f32, pad1: f32,
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

  // base stream: left→right, lanes of slightly different speed, gentle weave
  let lane = 0.5 + 0.5 * sin(pt.pos.y * 7.0 + h1 * 6.2832);
  // "target" is a reserved word in WGSL — hence "goal"
  let goal = vec2<f32>(
    P.stream * (0.55 + 0.9 * lane),
    0.05 * sin(P.time * 0.4 + pt.pos.x * 2.5 + h2 * 6.2832)
  );
  var v = pt.vel + (goal - pt.vel) * min(3.0 * P.dt, 1.0);

  // curl-ish wander so the stream shimmers
  v += vec2<f32>(
    sin(pt.pos.y * 6.0 + P.time * 0.8 + h2 * 6.2832),
    cos(pt.pos.x * 5.0 - P.time * 0.6 + h1 * 6.2832)
  ) * 0.18 * P.dt;

  // obstacle deflection: push away from glyphs along the field gradient.
  let f = fieldAt(pt.pos);
  if (f > 0.004) {
    let e = 0.012;
    let gx = fieldAt(pt.pos + vec2<f32>(e, 0.0)) - fieldAt(pt.pos - vec2<f32>(e, 0.0));
    let gy = fieldAt(pt.pos + vec2<f32>(0.0, e)) - fieldAt(pt.pos - vec2<f32>(0.0, e));
    let g = vec2<f32>(gx, gy);
    let gl = length(g);
    if (gl > 1e-5) {
      let n = g / gl;
      // away from the rock, stronger the deeper in the field you are
      v -= n * P.push * (f * f * 4.0 + f * 0.6) * P.dt;
      // slide: damp the into-rock velocity component so flow hugs the surface
      let into = dot(v, n);
      if (into > 0.0) { v -= n * into * min(8.0 * f * P.dt, 0.9); }
    }
    // deep inside (phrase just changed): strong ejection + damping
    if (f > 0.55) { v *= 1.0 - min(3.0 * P.dt, 0.5); }
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

  // wrap: exit right → re-enter left at a fresh lane; soft vertical wrap
  if (pos.x > 1.12) {
    pos.x = -1.12;
    pos.y = rand01(i + u32(P.time * 16.0) * 2659u) * 2.0 - 1.0;
    v = vec2<f32>(P.stream, 0.0);
  }
  if (pos.x < -1.14) { pos.x = 1.10; }
  if (pos.y > 1.08) { pos.y = -1.06; }
  if (pos.y < -1.08) { pos.y = 1.06; }

  pt.pos = pos;
  pt.vel = v;
  parts[i] = pt;
}
"#;

// Render: riverbed backdrop (normal-mapped, glyph-embossed) + instanced
// soft-quad particles colored from the warm half of a normal-map palette.
const DRAW_SHADER: &str = r#"
struct Params {
  res: vec2<f32>, mouse: vec2<f32>,
  time: f32, dt: f32, count: u32, stream: f32,
  push: f32, mousef: f32, dpr: f32, pad1: f32,
};
@group(0) @binding(0) var<uniform> P: Params;
@group(0) @binding(1) var field: texture_2d<f32>;
@group(0) @binding(2) var fsamp: sampler;

// ---------- riverbed backdrop ----------
@vertex
fn vs_bg(@builtin(vertex_index) i: u32) -> @builtin(position) vec4<f32> {
  var p = array<vec2<f32>, 3>(vec2<f32>(-1.,-1.), vec2<f32>(3.,-1.), vec2<f32>(-1.,3.));
  return vec4<f32>(p[i], 0., 1.);
}
fn bedHeight(p: vec2<f32>, t: f32) -> f32 {
  return sin(p.x * 4.0 + t) * 0.5 + cos(p.y * 5.0 - t * 0.8) * 0.5
       + sin((p.x + p.y) * 8.0 + t * 0.5) * 0.3;
}
@fragment
fn fs_bg(@builtin(position) frag: vec4<f32>) -> @location(0) vec4<f32> {
  let uv = frag.xy / P.res;
  let aspect = P.res.x / P.res.y;
  let p = vec2<f32>((uv.x - 0.5) * aspect, uv.y - 0.5) * 3.0;
  let t = P.time * 0.25;

  // dim normal-mapped riverbed
  let e = 0.02;
  let hC = bedHeight(p, t);
  let dx = hC - bedHeight(p + vec2<f32>(e, 0.0), t);
  let dy = hC - bedHeight(p + vec2<f32>(0.0, e), t);
  let n = normalize(vec3<f32>(dx * 5.0, dy * 5.0, 1.0));
  let l = normalize(vec3<f32>(cos(t * 0.6) * 0.7, sin(t * 0.6) * 0.7, 0.75));
  let diff = max(dot(n, l), 0.0);
  let umber = vec3<f32>(0.068, 0.046, 0.026);
  let moss  = vec3<f32>(0.030, 0.056, 0.034);
  var col = mix(umber, moss, clamp(hC * 0.5 + 0.5, 0.0, 1.0)) * (0.45 + 0.95 * diff);

  // glyph emboss: the words darken the bed and catch a warm rim at their edges
  let fuv = uv;
  let f  = textureSampleLevel(field, fsamp, fuv, 0.0).r;
  let ef = 1.5 / P.res.x;
  let gx = textureSampleLevel(field, fsamp, fuv + vec2<f32>(ef, 0.0), 0.0).r
         - textureSampleLevel(field, fsamp, fuv - vec2<f32>(ef, 0.0), 0.0).r;
  let gy = textureSampleLevel(field, fsamp, fuv + vec2<f32>(0.0, ef), 0.0).r
         - textureSampleLevel(field, fsamp, fuv - vec2<f32>(0.0, ef), 0.0).r;
  col *= 1.0 - 0.6 * f;
  let rim = clamp(length(vec2<f32>(gx, gy)) * 10.0, 0.0, 1.0) * (1.0 - f);
  col += rim * vec3<f32>(0.085, 0.062, 0.030);

  return vec4<f32>(col, 1.0);
}

// ---------- particles: instanced soft quads, additive ----------
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
  // flow direction is encoded exactly like a normal map's RG channels;
  // the blue (z/flat) channel is suppressed, leaving the warm rim palette:
  // salmon (→), lime (↑), gold (↗), deep teal-green (←).
  // vertical deflection is exaggerated in the encoding so the parting flow
  // around glyphs shifts visibly lime (up) / crimson-salmon (down) vs. gold
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

  // ── noon sparkle: stalled particles (upstream faces) glint hard ──
  let h1 = rand01(ii);
  let h2 = rand01(ii ^ 0x68bc21ebu);
  let tw = pow(max(sin(P.time * (2.0 + h1 * 7.0) + h2 * 6.2832), 0.0), 26.0);
  let stag = 1.0 - clamp(speed / max(P.stream, 0.01), 0.0, 1.0);
  // sparkle concentrates where the stream stalls (accumulation), with only a
  // faint global shimmer; clamp so trapped particles can't blow out white
  let spark = min(tw * (0.03 + 1.8 * stag * stag), 1.1);
  col = mix(col, vec3<f32>(1.0, 0.96, 0.82), clamp(spark, 0.0, 0.8));
  lum += spark * 1.5;
  lum = min(lum, 2.4);

  let px = vec2<f32>(2.0, 2.0) / P.res;
  let size = (1.5 + h2 * 0.8 + spark * 4.0) * max(P.dpr, 1.0);
  // motion-stretch: fast particles smear into silky streamlines along their
  // velocity; stalled (sparkling) ones stay round — water silk vs. sun glints
  let stretch = size + min(speed * 26.0, 11.0) * max(P.dpr, 1.0) * (1.0 - clamp(spark, 0.0, 1.0));
  let along = dir * stretch;
  let perp = vec2<f32>(-dir.y, dir.x) * size;
  let off = (corners[vi].x * along + corners[vi].y * perp) * px;
  var o: VOut;
  o.pos = vec4<f32>(ppos + off, 0.0, 1.0);
  o.col = col * lum * 0.14;
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

fn rnd(s: &mut u32) -> f32 {
    *s ^= *s << 13;
    *s ^= *s >> 17;
    *s ^= *s << 5;
    (*s as f32) / (u32::MAX as f32)
}

fn set_status(text: &str) {
    if let Some(el) = web_sys::window()
        .and_then(|w| w.document())
        .and_then(|d| d.get_element_by_id("fps"))
    {
        el.set_text_content(Some(text));
    }
}

// Rasterize LINE1 + the current phrase into the obstacle field: white text on
// black, drawn twice (wide blur halo + tight core) so the field falls off
// smoothly around the glyphs — that falloff IS the deflection force.
fn raster_field(
    ctx: &web_sys::CanvasRenderingContext2d,
    w: u32,
    h: u32,
    line2: &str,
) -> Vec<u8> {
    let (wf, hf) = (w as f64, h as f64);
    ctx.set_filter("none");
    ctx.set_fill_style_str("#000000");
    ctx.fill_rect(0.0, 0.0, wf, hf);
    ctx.set_fill_style_str("#ffffff");
    ctx.set_text_align("center");
    ctx.set_text_baseline("middle");

    let f1 = format!("900 {:.0}px -apple-system, system-ui, sans-serif", wf * 0.118);
    let f2 = format!("800 {:.0}px -apple-system, system-ui, sans-serif", wf * 0.054);
    let y1 = hf * 0.40;
    let y2 = hf * 0.625;

    // wide soft halo (the "pressure wave" ahead of the rock)
    ctx.set_filter("blur(10px)");
    ctx.set_font(&f1);
    ctx.fill_text(LINE1, wf / 2.0, y1).ok();
    ctx.set_font(&f2);
    ctx.fill_text(line2, wf / 2.0, y2).ok();
    // tight core (the rock itself)
    ctx.set_filter("blur(2px)");
    ctx.set_font(&f1);
    ctx.fill_text(LINE1, wf / 2.0, y1).ok();
    ctx.set_font(&f2);
    ctx.fill_text(line2, wf / 2.0, y2).ok();
    ctx.set_filter("none");

    let img = ctx.get_image_data(0.0, 0.0, wf, hf).unwrap();
    let data = img.data();
    let mut out = Vec::with_capacity((w * h) as usize);
    for i in 0..(w * h) as usize {
        out.push(data[i * 4]); // red channel = coverage
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
    let width = (css_w * dpr) as u32;
    let height = (css_h * dpr) as u32;
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

    // ---- buffers & textures ----
    let mut rng = 0x9e3779b9u32;
    let mut init: Vec<f32> = Vec::with_capacity(PARTICLES as usize * 4);
    for _ in 0..PARTICLES {
        init.push(rnd(&mut rng) * 2.2 - 1.1); // pos.x — pre-spread across screen
        init.push(rnd(&mut rng) * 2.0 - 1.0); // pos.y
        init.push(0.25 + rnd(&mut rng) * 0.2); // vel.x — already streaming
        init.push(0.0); // vel.y
    }
    let particle_buf = device.create_buffer(&wgpu::BufferDescriptor {
        label: Some("particles"),
        size: (PARTICLES as u64) * 16,
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
        format: wgpu::TextureFormat::R8Unorm,
        usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
        view_formats: &[],
    });
    let field_view = field_tex.create_view(&wgpu::TextureViewDescriptor::default());
    let field_samp = device.create_sampler(&wgpu::SamplerDescriptor {
        label: Some("field-samp"),
        address_mode_u: wgpu::AddressMode::ClampToEdge,
        address_mode_v: wgpu::AddressMode::ClampToEdge,
        mag_filter: wgpu::FilterMode::Linear,
        min_filter: wgpu::FilterMode::Linear,
        ..Default::default()
    });

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
                bytes_per_row: Some(fw),
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
        &raster_field(&fctx, field_w, field_h, PHRASES[0]),
    );

    // ---- bind groups ----
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
    let common_bg = device.create_bind_group(&wgpu::BindGroupDescriptor {
        label: None,
        layout: &common_bgl,
        entries: &[
            wgpu::BindGroupEntry { binding: 0, resource: param_buf.as_entire_binding() },
            wgpu::BindGroupEntry { binding: 1, resource: wgpu::BindingResource::TextureView(&field_view) },
            wgpu::BindGroupEntry { binding: 2, resource: wgpu::BindingResource::Sampler(&field_samp) },
        ],
    });
    let parts_bg = device.create_bind_group(&wgpu::BindGroupDescriptor {
        label: None,
        layout: &parts_bgl,
        entries: &[wgpu::BindGroupEntry { binding: 0, resource: particle_buf.as_entire_binding() }],
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

    // ---- pipelines ----
    let sim_pipeline = device.create_compute_pipeline(&wgpu::ComputePipelineDescriptor {
        label: Some("sim"),
        layout: Some(&compute_pl),
        module: &sim_mod,
        entry_point: Some("cs"),
        compilation_options: Default::default(),
        cache: None,
    });

    let bg_pipeline = device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
        label: Some("bg"),
        layout: Some(&render_pl),
        vertex: wgpu::VertexState {
            module: &draw_mod,
            entry_point: Some("vs_bg"),
            buffers: &[],
            compilation_options: Default::default(),
        },
        fragment: Some(wgpu::FragmentState {
            module: &draw_mod,
            entry_point: Some("fs_bg"),
            targets: &[Some(format.into())],
            compilation_options: Default::default(),
        }),
        primitive: wgpu::PrimitiveState::default(),
        depth_stencil: None,
        multisample: wgpu::MultisampleState::default(),
        multiview_mask: None,
        cache: None,
    });

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
                format,
                blend: Some(wgpu::BlendState {
                    color: wgpu::BlendComponent {
                        src_factor: wgpu::BlendFactor::One,
                        dst_factor: wgpu::BlendFactor::One,
                        operation: wgpu::BlendOperation::Add,
                    },
                    alpha: wgpu::BlendComponent {
                        src_factor: wgpu::BlendFactor::One,
                        dst_factor: wgpu::BlendFactor::One,
                        operation: wgpu::BlendOperation::Add,
                    },
                }),
                write_mask: wgpu::ColorWrites::ALL,
            })],
            compilation_options: Default::default(),
        }),
        primitive: wgpu::PrimitiveState::default(), // triangle list
        depth_stencil: None,
        multisample: wgpu::MultisampleState::default(),
        multiview_mask: None,
        cache: None,
    });

    // ---- frame loop ----
    let groups = (PARTICLES + WG - 1) / WG;
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
                PARTICLES / 1000
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
                &raster_field(&fctx, field_w, field_h, PHRASES[phrase_idx]),
            );
        }

        let (mx, my, mact) = mouse_r.get();
        let params = Params {
            res: [width as f32, height as f32],
            mouse: [mx, my],
            time: ((now - t0) / 1000.0) as f32,
            dt,
            count: PARTICLES,
            stream: 0.32,
            push: 2.3,
            mousef: 0.5 * mact,
            dpr: dpr as f32,
            _pad: 0.0,
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
            {
                let mut rp = enc.begin_render_pass(&wgpu::RenderPassDescriptor {
                    label: None,
                    color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                        view: &view,
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
                });
                rp.set_bind_group(0, &common_bg, &[]);
                rp.set_pipeline(&bg_pipeline);
                rp.draw(0..3, 0..1);
                rp.set_pipeline(&p_pipeline);
                rp.set_vertex_buffer(0, particle_buf.slice(..));
                rp.draw(0..6, 0..PARTICLES);
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
