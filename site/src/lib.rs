//! The public site, drawn with wgpu on one full-screen canvas.
//!
//! The Go server renders the page shell with a semantic HTML *transcript* of the
//! CMS content (for search engines, screen readers, and browsers without
//! WebGPU). This app reads the same content as JSON from /api/site.json and
//! draws the experience. `<html class="gpu">` hides the transcript visually;
//! if WebGPU fails here, the class is removed and the transcript shows instead.

use std::cell::RefCell;
use std::rc::Rc;

use serde::Deserialize;
use wasm_bindgen::prelude::*;
use wasm_bindgen::JsCast;
use wasm_bindgen_futures::JsFuture;

#[derive(Debug, Deserialize)]
pub struct SiteData {
    pub pages: Vec<Page>,
    pub experiments: Vec<Experiment>,
}

#[derive(Debug, Deserialize)]
pub struct Page {
    pub slug: String,
    pub title: String,
    pub body: String,
}

#[derive(Debug, Deserialize)]
pub struct Experiment {
    pub slug: String,
    pub title: String,
    pub summary: String,
}

// placeholder scene: a slow gradient field. Replace with the real site.
const SHADER: &str = r#"
struct U { time: f32, aspect: f32, _p0: f32, _p1: f32 };
@group(0) @binding(0) var<uniform> u: U;

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
    return vec4<f32>(c, c * 0.9, c * 1.2, 1.0);
}
"#;

#[wasm_bindgen(start)]
pub fn start() {
    console_error_panic_hook::set_once();
    wasm_bindgen_futures::spawn_local(async {
        if let Err(e) = run().await {
            web_sys::console::error_1(&e);
            fall_back();
        }
    });
}

/// Show the HTML transcript instead of the canvas.
fn fall_back() {
    if let Some(root) = web_sys::window()
        .and_then(|w| w.document())
        .and_then(|d| d.document_element())
    {
        let _ = root.class_list().remove_1("gpu");
    }
}

async fn fetch_site() -> Result<SiteData, JsValue> {
    let window = web_sys::window().ok_or("no window")?;
    let resp: web_sys::Response = JsFuture::from(window.fetch_with_str("/api/site.json"))
        .await?
        .dyn_into()?;
    if !resp.ok() {
        return Err(format!("/api/site.json: HTTP {}", resp.status()).into());
    }
    let text = JsFuture::from(resp.text()?).await?;
    let text = text.as_string().ok_or("site.json: not text")?;
    serde_json::from_str(&text).map_err(|e| JsValue::from_str(&e.to_string()))
}

async fn run() -> Result<(), JsValue> {
    let window = web_sys::window().ok_or("no window")?;
    let document = window.document().ok_or("no document")?;
    let canvas: web_sys::HtmlCanvasElement = document
        .get_element_by_id("stage")
        .ok_or("no #stage canvas")?
        .dyn_into()?;

    let site = fetch_site().await?;
    web_sys::console::log_1(
        &format!("site: {} pages, {} experiments", site.pages.len(), site.experiments.len()).into(),
    );

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
        .map_err(|e| e.to_string())?;
    let adapter = instance
        .request_adapter(&wgpu::RequestAdapterOptions {
            power_preference: wgpu::PowerPreference::HighPerformance,
            force_fallback_adapter: false,
            compatible_surface: Some(&surface),
        })
        .await
        .map_err(|e| format!("WebGPU adapter unavailable: {e}"))?;
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
        .map_err(|e| e.to_string())?;

    let caps = surface.get_capabilities(&adapter);
    let format = caps.formats[0];
    let mut config = wgpu::SurfaceConfiguration {
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

    let uniform = device.create_buffer(&wgpu::BufferDescriptor {
        label: Some("u"),
        size: 16,
        usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
        mapped_at_creation: false,
    });
    let bgl = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
        label: None,
        entries: &[wgpu::BindGroupLayoutEntry {
            binding: 0,
            visibility: wgpu::ShaderStages::FRAGMENT,
            ty: wgpu::BindingType::Buffer {
                ty: wgpu::BufferBindingType::Uniform,
                has_dynamic_offset: false,
                min_binding_size: None,
            },
            count: None,
        }],
    });
    let bg = device.create_bind_group(&wgpu::BindGroupDescriptor {
        label: None,
        layout: &bgl,
        entries: &[wgpu::BindGroupEntry { binding: 0, resource: uniform.as_entire_binding() }],
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

    let perf = window.performance().ok_or("no performance")?;
    let t0 = perf.now();

    // frame loop: resize-aware, one full-screen pass
    let f: Rc<RefCell<Option<Closure<dyn FnMut()>>>> = Rc::new(RefCell::new(None));
    let g = f.clone();
    let win = window.clone();
    *g.borrow_mut() = Some(Closure::new(move || {
        let (w, h) = size(&canvas);
        if w != config.width || h != config.height {
            canvas.set_width(w);
            canvas.set_height(h);
            config.width = w;
            config.height = h;
            surface.configure(&device, &config);
        }
        let t = ((perf.now() - t0) / 1000.0) as f32;
        let u: [f32; 4] = [t, w as f32 / h as f32, 0.0, 0.0];
        queue.write_buffer(&uniform, 0, bytemuck::cast_slice(&u));

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
                rp.set_bind_group(0, &bg, &[]);
                rp.draw(0..3, 0..1);
            }
            queue.submit([enc.finish()]);
            frame.present();
        }
        win.request_animation_frame(f.borrow().as_ref().unwrap().as_ref().unchecked_ref())
            .unwrap();
    }));
    window.request_animation_frame(g.borrow().as_ref().unwrap().as_ref().unchecked_ref())?;
    Ok(())
}
