//! `builtin/site`: the default site front end, drawn with wgpu on one
//! full-screen canvas.
//!
//! It runs inside the site's sandboxed iframe (docs/frontend-protocol.md). The
//! host API script `/site-host.js` defines `window.site`: the CMS content and
//! current route arrive through `site.loaded`, route changes through
//! `site.onRoute`, and navigation goes back out through `site.navigate`. The
//! parent page owns the URL and the accessible HTML transcript; this app draws
//! the current page's title and body (plus a clickable list of pages) over a
//! placeholder shader scene.
//!
//! Without `window.site` (standalone, e.g. `trunk serve`) it uses placeholder
//! content and navigates locally.

use std::cell::RefCell;
use std::rc::Rc;
use std::sync::Arc;

use serde::Deserialize;
use wasm_bindgen::prelude::*;
use wasm_bindgen::JsCast;
use wasm_bindgen_futures::JsFuture;

#[derive(Debug, Default, Clone, Deserialize)]
#[serde(default)]
pub struct SiteData {
    pub pages: Vec<Page>,
    pub experiments: Vec<Experiment>,
}

#[derive(Debug, Default, Clone, Deserialize)]
#[serde(default)]
pub struct Page {
    pub slug: String,
    pub title: String,
    pub body: String,
}

#[derive(Debug, Default, Clone, Deserialize)]
#[serde(default)]
pub struct Experiment {
    pub slug: String,
    pub title: String,
    pub summary: String,
}

/// What `site.loaded` resolves with.
#[derive(Debug, Default, Deserialize)]
#[serde(default)]
struct Loaded {
    content: SiteData,
    route: String,
}

// placeholder scene: a slow gradient field, with the page layer (text drawn by
// site.textCanvas, premultiplied alpha) composited over it. Replace the
// background with the real site.
const SHADER: &str = r#"
struct U { time: f32, aspect: f32, scroll: f32, _p: f32 };
@group(0) @binding(0) var<uniform> u: U;
@group(0) @binding(1) var page: texture_2d<f32>;

@vertex
fn vs(@builtin(vertex_index) i: u32) -> @builtin(position) vec4<f32> {
    let p = vec2<f32>(f32((i << 1u) & 2u), f32(i & 2u));
    return vec4<f32>(p * 2.0 - 1.0, 0.0, 1.0);
}

@fragment
fn fs(@builtin(position) pos: vec4<f32>) -> @location(0) vec4<f32> {
    let t = u.time * 0.1;
    let v = sin(pos.x * 0.004 + t) + sin(pos.y * 0.005 - t * 1.3);
    let c = 0.06 + 0.04 * v;
    let bg = vec3<f32>(c, c * 0.9, c * 1.2);
    let dim = vec2<i32>(textureDimensions(page));
    let p = vec2<i32>(i32(pos.x), i32(pos.y + u.scroll));
    var layer = vec4<f32>(0.0);
    if (p.x >= 0 && p.y >= 0 && p.x < dim.x && p.y < dim.y) {
        layer = textureLoad(page, p, 0);
    }
    return vec4<f32>(bg * (1.0 - layer.a) + layer.rgb, 1.0);
}
"#;

const INK: &str = "#e8e2d6";
const DIM: &str = "#9a948a";
const ACCENT: &str = "#f2a35e";
const SANS: &str = "system-ui, -apple-system, \"Segoe UI\", Roboto, sans-serif";
const SERIF: &str = "Georgia, \"Times New Roman\", serif";

// ── host API (window.site) ──────────────────────────────────────────────────

fn host() -> Option<JsValue> {
    let w = web_sys::window()?;
    let site = js_sys::Reflect::get(&w, &"site".into()).ok()?;
    site.is_object().then_some(site)
}

fn host_fn(name: &str) -> Option<(JsValue, js_sys::Function)> {
    let site = host()?;
    let f = js_sys::Reflect::get(&site, &name.into()).ok()?;
    let f = f.dyn_into::<js_sys::Function>().ok()?;
    Some((site, f))
}

fn host_call(name: &str, args: &[JsValue]) -> Option<JsValue> {
    let (site, f) = host_fn(name)?;
    let args: js_sys::Array = args.iter().collect();
    f.apply(&site, &args).ok()
}

/// console + `site.reportError(err, kind)`. Before `site.ready()` any report
/// makes the parent fall back; `gpu-lost` always does.
fn report(msg: &str, kind: &str) {
    web_sys::console::error_1(&msg.into());
    host_call("reportError", &[js_sys::Error::new(msg).into(), kind.into()]);
}

/// The route from an onRoute callback argument (a slug string, or an object
/// with `route`), falling back to `site.route`.
fn route_from(arg: &JsValue) -> Option<String> {
    arg.as_string()
        .or_else(|| {
            js_sys::Reflect::get(arg, &"route".into())
                .ok()
                .and_then(|v| v.as_string())
        })
        .or_else(|| {
            host().and_then(|s| js_sys::Reflect::get(&s, &"route".into()).ok()?.as_string())
        })
}

async fn load_content() -> Loaded {
    let Some(site) = host() else {
        web_sys::console::info_1(&"site: no window.site (standalone) — placeholder content".into());
        return placeholder();
    };
    let loaded = async {
        let p = js_sys::Reflect::get(&site, &"loaded".into())?.dyn_into::<js_sys::Promise>()?;
        let v = JsFuture::from(p).await?;
        let json: String = js_sys::JSON::stringify(&v)?.into();
        serde_json::from_str::<Loaded>(&json).map_err(|e| JsValue::from_str(&e.to_string()))
    };
    match loaded.await {
        Ok(l) => l,
        Err(e) => {
            web_sys::console::error_2(&"site.loaded failed; using placeholder content:".into(), &e);
            placeholder()
        }
    }
}

fn placeholder() -> Loaded {
    let page = |slug: &str, title: &str, body: &str| Page {
        slug: slug.into(),
        title: title.into(),
        body: body.into(),
    };
    Loaded {
        content: SiteData {
            pages: vec![
                page("", "Home", "Placeholder content: this front end is running standalone, without the site host.\n\nInside the site, the CMS content arrives through window.site."),
                page("about", "About", "A second placeholder page, to exercise navigation."),
            ],
            experiments: vec![Experiment {
                slug: "particle-stream".into(),
                title: "particle-stream".into(),
                summary: "Words as rocks in a stream.".into(),
            }],
        },
        route: String::new(),
    }
}

// ── page layer: CMS text → 2D canvas → texture ──────────────────────────────

/// A clickable nav label, in page-layer (device px) coordinates.
struct Hit {
    x: f64,
    y: f64,
    w: f64,
    h: f64,
    slug: String,
}

struct State {
    data: SiteData,
    route: String,
    scroll: f64, // device px
    content_h: f64,
    hits: Vec<Hit>,
    dirty: bool,
}

fn size_of(c: &JsValue) -> (f64, f64) {
    let get = |k: &str| {
        js_sys::Reflect::get(c, &k.into())
            .ok()
            .and_then(|v| v.as_f64())
            .unwrap_or(0.0)
    };
    (get("width"), get("height"))
}

fn canvas_2d(
    doc: &web_sys::Document,
    w: u32,
    h: u32,
) -> Result<(web_sys::HtmlCanvasElement, web_sys::CanvasRenderingContext2d), JsValue> {
    let c: web_sys::HtmlCanvasElement = doc.create_element("canvas")?.dyn_into()?;
    c.set_width(w.max(1));
    c.set_height(h.max(1));
    let ctx: web_sys::CanvasRenderingContext2d =
        c.get_context("2d")?.ok_or("no 2d context")?.dyn_into()?;
    Ok((c, ctx))
}

/// Text → canvas. Uses `site.textCanvas` when the host provides it, else a
/// local word-wrapping fallback (standalone).
fn text_block(
    doc: &web_sys::Document,
    text: &str,
    px: f64,
    weight: &str,
    family: &str,
    color: &str,
    max_width: f64,
) -> Result<JsValue, JsValue> {
    let font = format!("{weight} {px:.0}px {family}");
    if let Some((site, f)) = host_fn("textCanvas") {
        let opts = js_sys::Object::new();
        js_sys::Reflect::set(&opts, &"font".into(), &font.as_str().into())?;
        js_sys::Reflect::set(&opts, &"color".into(), &color.into())?;
        js_sys::Reflect::set(&opts, &"maxWidth".into(), &max_width.into())?;
        if let Ok(c) = f.call2(&site, &text.into(), &opts) {
            if c.is_object() {
                return Ok(c);
            }
        }
    }
    // fallback: greedy word wrap on a 2D canvas
    let (_, probe) = canvas_2d(doc, 1, 1)?;
    probe.set_font(&font);
    let mut lines: Vec<String> = Vec::new();
    for para in text.split('\n') {
        let mut line = String::new();
        for word in para.split_whitespace() {
            let cand = if line.is_empty() { word.to_string() } else { format!("{line} {word}") };
            if !line.is_empty() && probe.measure_text(&cand)?.width() > max_width {
                lines.push(std::mem::replace(&mut line, word.to_string()));
            } else {
                line = cand;
            }
        }
        lines.push(line);
    }
    let lh = (px * 1.4).ceil();
    let mut w: f64 = 1.0;
    for l in &lines {
        w = w.max(probe.measure_text(l)?.width().ceil());
    }
    let (c, ctx) = canvas_2d(doc, w as u32, (lh * lines.len() as f64) as u32)?;
    ctx.set_font(&font);
    ctx.set_text_baseline("top");
    ctx.set_fill_style_str(color);
    for (i, l) in lines.iter().enumerate() {
        ctx.fill_text(l, 0.0, i as f64 * lh + (lh - px) / 2.0)?;
    }
    Ok(c.into())
}

/// Lay out the current page into one canvas `w` wide: nav row of page titles,
/// then the page title and body. Returns the canvas, its height and the nav
/// hit rects.
fn compose(
    doc: &web_sys::Document,
    w: u32,
    max_h: u32,
    dpr: f64,
    data: &SiteData,
    route: &str,
) -> Result<(web_sys::HtmlCanvasElement, u32, Vec<Hit>), JsValue> {
    let wf = w as f64;
    let pad = if wf / dpr < 600.0 { 20.0 } else { 48.0 } * dpr;
    let col = (wf - 2.0 * pad).min(720.0 * dpr).max(40.0 * dpr);
    let mut blocks: Vec<(JsValue, f64, f64)> = Vec::new();
    let mut hits = Vec::new();
    let mut y = pad;

    // nav: one label per page, current in the accent colour; wraps
    let mut x = pad;
    let mut row_h: f64 = 0.0;
    for p in &data.pages {
        let label = if !p.title.trim().is_empty() {
            p.title.trim()
        } else if p.slug.is_empty() {
            "Home"
        } else {
            p.slug.as_str()
        };
        let color = if p.slug == route { ACCENT } else { DIM };
        let c = text_block(doc, label, 15.0 * dpr, "600", SANS, color, col)?;
        let (cw, ch) = size_of(&c);
        if x > pad && x + cw > wf - pad {
            x = pad;
            y += row_h + 8.0 * dpr;
            row_h = 0.0;
        }
        hits.push(Hit { x, y, w: cw, h: ch, slug: p.slug.clone() });
        blocks.push((c, x, y));
        x += cw + 24.0 * dpr;
        row_h = row_h.max(ch);
    }
    if !data.pages.is_empty() {
        y += row_h + 40.0 * dpr;
    }

    // the current page (or experiment); unknown routes get a small notice
    let (title, body) = if let Some(p) = data.pages.iter().find(|p| p.slug == route) {
        (p.title.clone(), p.body.clone())
    } else if let Some(e) = data.experiments.iter().find(|e| e.slug == route) {
        (e.title.clone(), e.summary.clone())
    } else {
        ("Not found".to_string(), format!("There is no page at /{route}."))
    };
    let mut push = |text: &str, px: f64, weight: &str, family: &str, color: &str, before: f64, gap: f64| {
        if text.trim().is_empty() {
            return Ok::<(), JsValue>(());
        }
        y += before * dpr;
        let c = text_block(doc, text.trim(), px * dpr, weight, family, color, col)?;
        let (_, ch) = size_of(&c);
        blocks.push((c, pad, y));
        y += ch + gap * dpr;
        Ok(())
    };
    push(&title, 40.0, "600", SERIF, INK, 0.0, 20.0)?;
    // one block per paragraph, so line breaks survive whatever textCanvas does
    for para in body.split('\n') {
        push(para, 17.0, "400", SANS, INK, 0.0, 12.0)?;
    }
    if route.is_empty() && !data.experiments.is_empty() {
        push("EXPERIMENTS", 13.0, "600", SANS, DIM, 28.0, 10.0)?;
        for e in &data.experiments {
            let line = if e.summary.trim().is_empty() {
                e.title.clone()
            } else {
                format!("{} — {}", e.title, e.summary)
            };
            push(&line, 16.0, "400", SANS, INK, 0.0, 8.0)?;
        }
    }
    let total = ((y + pad).ceil() as u32).clamp(1, max_h);

    let (canvas, ctx) = canvas_2d(doc, w, total)?;
    let draw: js_sys::Function = js_sys::Reflect::get(&ctx, &"drawImage".into())?.dyn_into()?;
    for (c, bx, by) in &blocks {
        draw.call3(&ctx, c, &(*bx).into(), &(*by).into())?;
    }
    Ok((canvas, total, hits))
}

// ── app ──────────────────────────────────────────────────────────────────────

/// A startup failure, with the `site.reportError` kind to send.
struct Fail(String, &'static str);

impl From<JsValue> for Fail {
    fn from(e: JsValue) -> Self {
        let msg = e
            .dyn_ref::<js_sys::Error>()
            .map(|e| String::from(e.message()))
            .or_else(|| e.as_string())
            .unwrap_or_else(|| format!("{e:?}"));
        Fail(msg, "error")
    }
}

impl From<&str> for Fail {
    fn from(e: &str) -> Self {
        Fail(e.to_string(), "error")
    }
}

#[wasm_bindgen(start)]
pub fn start() {
    console_error_panic_hook::set_once();
    wasm_bindgen_futures::spawn_local(async {
        if let Err(Fail(msg, kind)) = run().await {
            report(&msg, kind);
        }
    });
}

async fn run() -> Result<(), Fail> {
    let window = web_sys::window().ok_or("no window")?;
    let document = window.document().ok_or("no document")?;
    let canvas: web_sys::HtmlCanvasElement = document
        .get_element_by_id("stage")
        .ok_or("no #stage canvas")?
        .dyn_into()
        .map_err(|_| "#stage is not a canvas")?;

    let dpr = window.device_pixel_ratio().min(2.0);
    let size = move |c: &web_sys::HtmlCanvasElement| {
        (
            ((c.client_width() as f64 * dpr) as u32).max(1),
            ((c.client_height() as f64 * dpr) as u32).max(1),
        )
    };
    let (width, height) = size(&canvas);
    canvas.set_width(width);
    canvas.set_height(height);

    let instance = wgpu::Instance::default();
    let surface = instance
        .create_surface(wgpu::SurfaceTarget::Canvas(canvas.clone()))
        .map_err(|e| Fail(format!("WebGPU surface: {e}"), "error"))?;
    let adapter = instance
        .request_adapter(&wgpu::RequestAdapterOptions {
            power_preference: wgpu::PowerPreference::HighPerformance,
            force_fallback_adapter: false,
            compatible_surface: Some(&surface),
        })
        .await
        .map_err(|e| Fail(format!("WebGPU adapter unavailable: {e}"), "error"))?;
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
        .map_err(|e| Fail(format!("WebGPU request_device failed: {e}"), "gpu-lost"))?;
    device.set_device_lost_callback(|reason, msg| {
        if reason != wgpu::DeviceLostReason::Destroyed {
            report(&format!("WebGPU device lost: {msg}"), "gpu-lost");
        }
    });
    // report validation errors instead of wgpu's default (panic)
    device.on_uncaptured_error(Arc::new(|e: wgpu::Error| {
        report(&format!("WebGPU error: {e}"), "error");
    }));
    let max_dim = device.limits().max_texture_dimension_2d;

    let caps = surface.get_capabilities(&adapter);
    let format = *caps.formats.first().ok_or("no surface formats")?;
    let mut config = wgpu::SurfaceConfiguration {
        usage: wgpu::TextureUsages::RENDER_ATTACHMENT,
        format,
        width: width.min(max_dim),
        height: height.min(max_dim),
        present_mode: wgpu::PresentMode::Fifo,
        desired_maximum_frame_latency: 2,
        alpha_mode: caps.alpha_modes[0],
        view_formats: vec![],
    };
    surface.configure(&device, &config);

    let uniform = device.create_buffer(&wgpu::BufferDescriptor {
        label: Some("u"),
        size: 16,
        usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
        mapped_at_creation: false,
    });
    let bgl = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
        label: None,
        entries: &[
            wgpu::BindGroupLayoutEntry {
                binding: 0,
                visibility: wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Buffer {
                    ty: wgpu::BufferBindingType::Uniform,
                    has_dynamic_offset: false,
                    min_binding_size: None,
                },
                count: None,
            },
            wgpu::BindGroupLayoutEntry {
                binding: 1,
                visibility: wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Texture {
                    sample_type: wgpu::TextureSampleType::Float { filterable: false },
                    view_dimension: wgpu::TextureViewDimension::D2,
                    multisampled: false,
                },
                count: None,
            },
        ],
    });
    let layout = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
        label: None,
        bind_group_layouts: &[Some(&bgl)],
        immediate_size: 0,
    });
    let module = device.create_shader_module(wgpu::ShaderModuleDescriptor {
        label: Some("scene"),
        source: wgpu::ShaderSource::Wgsl(SHADER.into()),
    });
    let pipeline = device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
        label: Some("scene"),
        layout: Some(&layout),
        vertex: wgpu::VertexState {
            module: &module,
            entry_point: Some("vs"),
            buffers: &[],
            compilation_options: Default::default(),
        },
        fragment: Some(wgpu::FragmentState {
            module: &module,
            entry_point: Some("fs"),
            targets: &[Some(format.into())],
            compilation_options: Default::default(),
        }),
        primitive: wgpu::PrimitiveState::default(),
        depth_stencil: None,
        multisample: wgpu::MultisampleState::default(),
        multiview_mask: None,
        cache: None,
    });

    // content: waits for the parent's site:init (via site.loaded)
    let loaded = load_content().await;
    let state = Rc::new(RefCell::new(State {
        data: loaded.content,
        route: loaded.route,
        scroll: 0.0,
        content_h: 0.0,
        hits: Vec::new(),
        dirty: true,
    }));

    // route changes from the parent (back/forward, or after our navigate)
    {
        let st = state.clone();
        let cb = Closure::<dyn FnMut(JsValue)>::new(move |arg: JsValue| {
            if let Some(r) = route_from(&arg) {
                let mut s = st.borrow_mut();
                if s.route != r {
                    s.route = r;
                    s.scroll = 0.0;
                    s.dirty = true;
                }
            }
        });
        if host_call("onRoute", &[cb.as_ref().clone()]).is_some() {
            // the route may have moved between site.loaded and subscribing
            if let Some(r) = host().and_then(|s| js_sys::Reflect::get(&s, &"route".into()).ok()?.as_string()) {
                let mut s = state.borrow_mut();
                if s.route != r {
                    s.route = r;
                    s.dirty = true;
                }
            }
        }
        cb.forget();
    }

    // input: click a nav label → navigate; wheel / touch drag → scroll
    let hit_at = {
        let st = state.clone();
        move |cx: f64, cy: f64| -> Option<String> {
            let s = st.borrow();
            let (x, y) = (cx * dpr, cy * dpr + s.scroll);
            s.hits
                .iter()
                .find(|h| x >= h.x && x < h.x + h.w && y >= h.y && y < h.y + h.h)
                .map(|h| h.slug.clone())
        }
    };
    {
        let st = state.clone();
        let hit_at = hit_at.clone();
        let cb = Closure::<dyn FnMut(web_sys::MouseEvent)>::new(move |e: web_sys::MouseEvent| {
            let Some(slug) = hit_at(e.client_x() as f64, e.client_y() as f64) else { return };
            if host_call("navigate", &[slug.as_str().into()]).is_none() {
                // standalone: no parent to own history, so switch locally
                let mut s = st.borrow_mut();
                s.route = slug;
                s.scroll = 0.0;
                s.dirty = true;
            }
        });
        canvas
            .add_event_listener_with_callback("click", cb.as_ref().unchecked_ref())
            .map_err(Fail::from)?;
        cb.forget();
    }
    {
        let c2 = canvas.clone();
        let cb = Closure::<dyn FnMut(web_sys::MouseEvent)>::new(move |e: web_sys::MouseEvent| {
            let over = hit_at(e.client_x() as f64, e.client_y() as f64).is_some();
            let _ = c2.style().set_property("cursor", if over { "pointer" } else { "default" });
        });
        canvas
            .add_event_listener_with_callback("mousemove", cb.as_ref().unchecked_ref())
            .map_err(Fail::from)?;
        cb.forget();
    }
    {
        let st = state.clone();
        let cb = Closure::<dyn FnMut(web_sys::WheelEvent)>::new(move |e: web_sys::WheelEvent| {
            let unit = match e.delta_mode() {
                1 => 16.0,  // lines
                2 => 400.0, // pages
                _ => 1.0,
            };
            st.borrow_mut().scroll += e.delta_y() * unit * dpr;
        });
        canvas
            .add_event_listener_with_callback("wheel", cb.as_ref().unchecked_ref())
            .map_err(Fail::from)?;
        cb.forget();
    }
    {
        let last_y = Rc::new(RefCell::new(None::<f64>));
        let ly = last_y.clone();
        let start = Closure::<dyn FnMut(web_sys::TouchEvent)>::new(move |e: web_sys::TouchEvent| {
            *ly.borrow_mut() = e.touches().get(0).map(|t| t.client_y() as f64);
        });
        let st = state.clone();
        let ly = last_y.clone();
        let mv = Closure::<dyn FnMut(web_sys::TouchEvent)>::new(move |e: web_sys::TouchEvent| {
            if let (Some(t), Some(prev)) = (e.touches().get(0), *ly.borrow()) {
                let y = t.client_y() as f64;
                st.borrow_mut().scroll += (prev - y) * dpr;
                *ly.borrow_mut() = Some(y);
            }
        });
        canvas
            .add_event_listener_with_callback("touchstart", start.as_ref().unchecked_ref())
            .map_err(Fail::from)?;
        canvas
            .add_event_listener_with_callback("touchmove", mv.as_ref().unchecked_ref())
            .map_err(Fail::from)?;
        start.forget();
        mv.forget();
    }

    let perf = window.performance().ok_or("no performance")?;
    let t0 = perf.now();
    let mut page: Option<(wgpu::Texture, wgpu::BindGroup)> = None;
    let mut ready = false;
    let mut compose_failed = false;

    // frame loop: resize-aware; recompose the page layer when dirty
    let f: Rc<RefCell<Option<Closure<dyn FnMut()>>>> = Rc::new(RefCell::new(None));
    let g = f.clone();
    let win = window.clone();
    *g.borrow_mut() = Some(Closure::new(move || {
        let (w, h) = size(&canvas);
        let (w, h) = (w.min(max_dim), h.min(max_dim));
        if w != config.width || h != config.height {
            canvas.set_width(w);
            canvas.set_height(h);
            config.width = w;
            config.height = h;
            surface.configure(&device, &config);
            state.borrow_mut().dirty = true;
        }

        let dirty = std::mem::take(&mut state.borrow_mut().dirty);
        if dirty || page.is_none() {
            let composed = {
                let s = state.borrow();
                compose(&document, w, max_dim, dpr, &s.data, &s.route)
            };
            match composed {
                Ok((layer, lh, hits)) => {
                    let tex = device.create_texture(&wgpu::TextureDescriptor {
                        label: Some("page"),
                        size: wgpu::Extent3d { width: w, height: lh, depth_or_array_layers: 1 },
                        mip_level_count: 1,
                        sample_count: 1,
                        dimension: wgpu::TextureDimension::D2,
                        format: wgpu::TextureFormat::Rgba8Unorm,
                        usage: wgpu::TextureUsages::TEXTURE_BINDING
                            | wgpu::TextureUsages::COPY_DST
                            | wgpu::TextureUsages::RENDER_ATTACHMENT,
                        view_formats: &[],
                    });
                    queue.copy_external_image_to_texture(
                        &wgpu::CopyExternalImageSourceInfo {
                            source: wgpu::ExternalImageSource::HTMLCanvasElement(layer),
                            origin: wgpu::Origin2d::ZERO,
                            flip_y: false,
                        },
                        wgpu::CopyExternalImageDestInfo {
                            texture: &tex,
                            mip_level: 0,
                            origin: wgpu::Origin3d::ZERO,
                            aspect: wgpu::TextureAspect::All,
                            color_space: wgpu::PredefinedColorSpace::Srgb,
                            premultiplied_alpha: true,
                        },
                        wgpu::Extent3d { width: w, height: lh, depth_or_array_layers: 1 },
                    );
                    let view = tex.create_view(&wgpu::TextureViewDescriptor::default());
                    let bg = device.create_bind_group(&wgpu::BindGroupDescriptor {
                        label: None,
                        layout: &bgl,
                        entries: &[
                            wgpu::BindGroupEntry { binding: 0, resource: uniform.as_entire_binding() },
                            wgpu::BindGroupEntry {
                                binding: 1,
                                resource: wgpu::BindingResource::TextureView(&view),
                            },
                        ],
                    });
                    let mut s = state.borrow_mut();
                    s.hits = hits;
                    s.content_h = lh as f64;
                    page = Some((tex, bg));
                }
                Err(e) => {
                    if !compose_failed {
                        compose_failed = true;
                        let msg = e.as_string().unwrap_or_else(|| format!("{e:?}"));
                        report(&format!("drawing the page failed: {msg}"), "error");
                    }
                }
            }
        }

        let scroll = {
            let mut s = state.borrow_mut();
            s.scroll = s.scroll.clamp(0.0, (s.content_h - h as f64).max(0.0));
            s.scroll
        };
        let t = ((perf.now() - t0) / 1000.0) as f32;
        let u: [f32; 4] = [t, w as f32 / h as f32, scroll as f32, 0.0];
        queue.write_buffer(&uniform, 0, bytemuck::cast_slice(&u));

        let frame = match surface.get_current_texture() {
            wgpu::CurrentSurfaceTexture::Success(t)
            | wgpu::CurrentSurfaceTexture::Suboptimal(t) => Some(t),
            _ => {
                surface.configure(&device, &config);
                None
            }
        };
        if let (Some(frame), Some((_, bg))) = (frame, page.as_ref()) {
            let view = frame.texture.create_view(&wgpu::TextureViewDescriptor::default());
            let mut enc = device.create_command_encoder(&wgpu::CommandEncoderDescriptor { label: None });
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
                rp.set_pipeline(&pipeline);
                rp.set_bind_group(0, bg, &[]);
                rp.draw(0..3, 0..1);
            }
            queue.submit([enc.finish()]);
            frame.present();
            if !ready {
                ready = true;
                host_call("ready", &[]);
            }
        }
        if let Some(cb) = f.borrow().as_ref() {
            let _ = win.request_animation_frame(cb.as_ref().unchecked_ref());
        }
    }));
    if let Some(cb) = g.borrow().as_ref() {
        window
            .request_animation_frame(cb.as_ref().unchecked_ref())
            .map_err(Fail::from)?;
    }
    Ok(())
}
