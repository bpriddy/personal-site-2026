use std::cell::RefCell;
use std::rc::Rc;
use wasm_bindgen::prelude::*;
use wasm_bindgen::JsCast;

// ─────────────────────────────────────────────────────────────────────────────
// Dense GPU-compute particle field that swarms to form text. Attraction points
// are sampled from the text rasterized to a hidden 2D canvas (opaque pixels →
// targets). Particle pos/vel live in a GPU storage buffer; a compute shader
// integrates them each frame (spring-to-target + curl jitter); the render pass
// reads the SAME buffer as vertices and draws additive points — so nothing round
// -trips to the CPU. Underneath, a full-screen normal-map lit layer adds a
// secondary color/light backdrop.
// ─────────────────────────────────────────────────────────────────────────────

const TEXT: &str = "BEN PRIDDY";
const PARTICLES: u32 = 250_000;
const WG: u32 = 64; // compute workgroup size

#[repr(C)]
#[derive(Clone, Copy, bytemuck::Pod, bytemuck::Zeroable)]
struct Params {
    res: [f32; 2],
    time: f32,
    dt: f32,
    count: u32,
    target_count: u32,
    spring: f32,
    damp: f32,
    noise: f32,
    _pad: [f32; 3],
}

const SHADER: &str = r#"
struct Particle { pos: vec2<f32>, vel: vec2<f32> };
struct Params {
  res: vec2<f32>, time: f32, dt: f32,
  count: u32, target_count: u32, spring: f32, damp: f32, noise: f32,
};

@group(0) @binding(0) var<storage, read_write> particles: array<Particle>;
@group(0) @binding(1) var<storage, read> targets: array<vec2<f32>>;
@group(0) @binding(2) var<uniform> P: Params;

// ---------- compute: integrate particles ----------
@compute @workgroup_size(64)
fn cs(@builtin(global_invocation_id) gid: vec3<u32>) {
  let i = gid.x;
  if (i >= P.count) { return; }
  var pt = particles[i];
  let tgt = targets[i % P.target_count];
  var v = pt.vel + (tgt - pt.pos) * P.spring * P.dt;
  let fi = f32(i) * 0.0002;
  v += vec2<f32>(sin(pt.pos.y * 3.0 + P.time + fi),
                 cos(pt.pos.x * 3.0 - P.time + fi)) * P.noise * P.dt;
  v *= P.damp;
  pt.vel = v;
  pt.pos = pt.pos + v * P.dt;
  particles[i] = pt;
}

// ---------- full-screen normal-map backdrop (secondary color + light) ----------
@vertex
fn vs_bg(@builtin(vertex_index) i: u32) -> @builtin(position) vec4<f32> {
  var p = array<vec2<f32>, 3>(vec2<f32>(-1.,-1.), vec2<f32>(3.,-1.), vec2<f32>(-1.,3.));
  return vec4<f32>(p[i], 0., 1.);
}
fn height(p: vec2<f32>, t: f32) -> f32 {
  return sin(p.x * 5.0 + t) * 0.5 + cos(p.y * 5.0 - t * 1.1) * 0.5
       + sin((p.x + p.y) * 9.0 + t * 0.7) * 0.25;
}
@fragment
fn fs_bg(@builtin(position) frag: vec4<f32>) -> @location(0) vec4<f32> {
  let uv = frag.xy / P.res;
  let aspect = P.res.x / P.res.y;
  let p = vec2<f32>((uv.x - 0.5) * aspect, uv.y - 0.5) * 3.0;
  let t = P.time * 0.4;
  let e = 0.015;
  let hC = height(p, t);
  let dx = hC - height(p + vec2<f32>(e, 0.0), t);
  let dy = hC - height(p + vec2<f32>(0.0, e), t);
  let n = normalize(vec3<f32>(dx * 6.0, dy * 6.0, 1.0));
  let l = normalize(vec3<f32>(cos(t * 0.7) * 0.8, sin(t * 0.7) * 0.8, 0.7));
  let diff = max(dot(n, l), 0.0);
  let cool = vec3<f32>(0.015, 0.03, 0.09);
  let warm = vec3<f32>(0.10, 0.05, 0.14);
  let base = mix(cool, warm, clamp(hC * 0.5 + 0.5, 0.0, 1.0));
  return vec4<f32>(base * (0.35 + 0.75 * diff), 1.0); // dim — particles are the star
}

// ---------- particles (additive points) ----------
struct VOut { @builtin(position) pos: vec4<f32>, @location(0) col: vec3<f32> };
@vertex
fn vs_p(@location(0) ppos: vec2<f32>) -> VOut {
  var o: VOut;
  o.pos = vec4<f32>(ppos, 0.0, 1.0);
  let h = 0.5 + 0.5 * sin(ppos.x * 2.2 + ppos.y * 1.7 + P.time * 0.5);
  let a = vec3<f32>(0.25, 0.6, 1.0);   // cyan
  let b = vec3<f32>(1.0, 0.55, 0.2);   // amber
  o.col = mix(a, b, h);
  return o;
}
@fragment
fn fs_p(in: VOut) -> @location(0) vec4<f32> {
  return vec4<f32>(in.col * 0.5, 1.0); // additive: overlaps build to bright
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

// Rasterize the text to a hidden 2D canvas and sample opaque pixels into NDC
// attraction points (aspect-corrected so the text isn't stretched on screen).
fn sample_text(text: &str, aspect: f32) -> Vec<[f32; 2]> {
    let doc = web_sys::window().unwrap().document().unwrap();
    let canvas: web_sys::HtmlCanvasElement =
        doc.create_element("canvas").unwrap().dyn_into().unwrap();
    let (w, h) = (1600u32, 320u32);
    canvas.set_width(w);
    canvas.set_height(h);
    let ctx: web_sys::CanvasRenderingContext2d =
        canvas.get_context("2d").unwrap().unwrap().dyn_into().unwrap();
    ctx.set_fill_style_str("#000000");
    ctx.fill_rect(0.0, 0.0, w as f64, h as f64);
    ctx.set_fill_style_str("#ffffff");
    ctx.set_font("bold 170px -apple-system, system-ui, sans-serif");
    ctx.set_text_align("center");
    ctx.set_text_baseline("middle");
    ctx.fill_text(text, (w as f64) / 2.0, (h as f64) / 2.0).ok();

    let img = ctx.get_image_data(0.0, 0.0, w as f64, h as f64).unwrap();
    let data = img.data();
    let mut pts = Vec::new();
    let stride = 3usize;
    let s = 1.0f32;
    let mut y = 0u32;
    while y < h {
        let mut x = 0u32;
        while x < w {
            let idx = ((y * w + x) * 4) as usize;
            if data[idx] > 100 {
                let u = (x as f32 / w as f32) * 2.0 - 1.0;
                let v = -((y as f32 / h as f32) * 2.0 - 1.0);
                let dy = v * (h as f32 / w as f32); // preserve text proportions
                pts.push([u * s / aspect, dy * s]);
            }
            x += stride as u32;
        }
        y += stride as u32;
    }
    pts
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
    let width = window.inner_width().unwrap().as_f64().unwrap() as u32;
    let height = window.inner_height().unwrap().as_f64().unwrap() as u32;
    canvas.set_width(width);
    canvas.set_height(height);
    let aspect = width as f32 / height as f32;

    let targets = sample_text(TEXT, aspect);
    if targets.is_empty() {
        set_status("no text targets sampled");
        return;
    }

    // ---- wgpu setup ----
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

    let shader = device.create_shader_module(wgpu::ShaderModuleDescriptor {
        label: Some("shader"),
        source: wgpu::ShaderSource::Wgsl(SHADER.into()),
    });

    // ---- buffers ----
    // particles: pos+vel, GPU-resident; both compute storage AND vertex source.
    let mut rng = 0x9e3779b9u32;
    let mut init: Vec<f32> = Vec::with_capacity(PARTICLES as usize * 4);
    for _ in 0..PARTICLES {
        init.push(rnd(&mut rng) * 2.0 - 1.0); // pos.x
        init.push(rnd(&mut rng) * 2.0 - 1.0); // pos.y
        init.push(0.0); // vel.x
        init.push(0.0); // vel.y
    }
    let particle_buf = device.create_buffer(&wgpu::BufferDescriptor {
        label: Some("particles"),
        size: (PARTICLES as u64) * 16,
        usage: wgpu::BufferUsages::STORAGE | wgpu::BufferUsages::VERTEX | wgpu::BufferUsages::COPY_DST,
        mapped_at_creation: false,
    });
    queue.write_buffer(&particle_buf, 0, bytemuck::cast_slice(&init));

    let target_buf = device.create_buffer(&wgpu::BufferDescriptor {
        label: Some("targets"),
        size: (targets.len() as u64) * 8,
        usage: wgpu::BufferUsages::STORAGE | wgpu::BufferUsages::COPY_DST,
        mapped_at_creation: false,
    });
    queue.write_buffer(&target_buf, 0, bytemuck::cast_slice(&targets));

    let param_buf = device.create_buffer(&wgpu::BufferDescriptor {
        label: Some("params"),
        size: std::mem::size_of::<Params>() as u64,
        usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
        mapped_at_creation: false,
    });

    // ---- bind group layouts ----
    fn storage_entry(binding: u32, read_only: bool) -> wgpu::BindGroupLayoutEntry {
        wgpu::BindGroupLayoutEntry {
            binding,
            visibility: wgpu::ShaderStages::COMPUTE,
            ty: wgpu::BindingType::Buffer {
                ty: wgpu::BufferBindingType::Storage { read_only },
                has_dynamic_offset: false,
                min_binding_size: None,
            },
            count: None,
        }
    }
    fn uniform_entry(binding: u32, vis: wgpu::ShaderStages) -> wgpu::BindGroupLayoutEntry {
        wgpu::BindGroupLayoutEntry {
            binding,
            visibility: vis,
            ty: wgpu::BindingType::Buffer {
                ty: wgpu::BufferBindingType::Uniform,
                has_dynamic_offset: false,
                min_binding_size: None,
            },
            count: None,
        }
    }

    let compute_bgl = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
        label: Some("compute"),
        entries: &[
            storage_entry(0, false),
            storage_entry(1, true),
            uniform_entry(2, wgpu::ShaderStages::COMPUTE),
        ],
    });
    let render_bgl = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
        label: Some("render"),
        entries: &[uniform_entry(2, wgpu::ShaderStages::VERTEX_FRAGMENT)],
    });

    let compute_bg = device.create_bind_group(&wgpu::BindGroupDescriptor {
        label: None,
        layout: &compute_bgl,
        entries: &[
            wgpu::BindGroupEntry { binding: 0, resource: particle_buf.as_entire_binding() },
            wgpu::BindGroupEntry { binding: 1, resource: target_buf.as_entire_binding() },
            wgpu::BindGroupEntry { binding: 2, resource: param_buf.as_entire_binding() },
        ],
    });
    let render_bg = device.create_bind_group(&wgpu::BindGroupDescriptor {
        label: None,
        layout: &render_bgl,
        entries: &[wgpu::BindGroupEntry { binding: 2, resource: param_buf.as_entire_binding() }],
    });

    let compute_pl = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
        label: None,
        bind_group_layouts: &[Some(&compute_bgl)],
        immediate_size: 0,
    });
    let render_pl = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
        label: None,
        bind_group_layouts: &[Some(&render_bgl)],
        immediate_size: 0,
    });

    // ---- pipelines ----
    let sim_pipeline = device.create_compute_pipeline(&wgpu::ComputePipelineDescriptor {
        label: Some("sim"),
        layout: Some(&compute_pl),
        module: &shader,
        entry_point: Some("cs"),
        compilation_options: Default::default(),
        cache: None,
    });

    let bg_pipeline = device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
        label: Some("bg"),
        layout: Some(&render_pl),
        vertex: wgpu::VertexState {
            module: &shader,
            entry_point: Some("vs_bg"),
            buffers: &[],
            compilation_options: Default::default(),
        },
        fragment: Some(wgpu::FragmentState {
            module: &shader,
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

    // particle vertex buffer reads pos (offset 0) from the 16-byte Particle stride
    let attrs = wgpu::vertex_attr_array![0 => Float32x2];
    let p_pipeline = device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
        label: Some("particles"),
        layout: Some(&render_pl),
        vertex: wgpu::VertexState {
            module: &shader,
            entry_point: Some("vs_p"),
            buffers: &[wgpu::VertexBufferLayout {
                array_stride: 16,
                step_mode: wgpu::VertexStepMode::Vertex,
                attributes: &attrs,
            }],
            compilation_options: Default::default(),
        },
        fragment: Some(wgpu::FragmentState {
            module: &shader,
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
        primitive: wgpu::PrimitiveState {
            topology: wgpu::PrimitiveTopology::PointList,
            ..Default::default()
        },
        depth_stencil: None,
        multisample: wgpu::MultisampleState::default(),
        multiview_mask: None,
        cache: None,
    });

    let groups = (PARTICLES + WG - 1) / WG;
    let target_count = targets.len() as u32; // captured Copy; keeps `targets` owned
    let perf = window.performance().unwrap();
    let t0 = perf.now();
    let mut last = t0;
    let mut frames: u32 = 0;
    let mut acc: f64 = 0.0;

    let f = Rc::new(RefCell::new(None::<Closure<dyn FnMut()>>));
    let g = f.clone();
    let win = window.clone();
    *g.borrow_mut() = Some(Closure::wrap(Box::new(move || {
        let now = perf.now();
        let dt_ms = now - last;
        last = now;
        let dt = (dt_ms / 1000.0).min(0.033) as f32;
        frames += 1;
        acc += dt_ms;
        if acc >= 250.0 {
            set_status(&format!(
                "{:.0} FPS · {} particles",
                frames as f64 * 1000.0 / acc,
                PARTICLES
            ));
            frames = 0;
            acc = 0.0;
        }

        let params = Params {
            res: [width as f32, height as f32],
            time: ((now - t0) / 1000.0) as f32,
            dt,
            count: PARTICLES,
            target_count,
            spring: 9.0,
            damp: 0.86,
            noise: 0.25,
            _pad: [0.0; 3],
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
                cp.set_bind_group(0, &compute_bg, &[]);
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
                rp.set_bind_group(0, &render_bg, &[]);
                rp.set_pipeline(&bg_pipeline);
                rp.draw(0..3, 0..1);
                rp.set_pipeline(&p_pipeline);
                rp.set_vertex_buffer(0, particle_buf.slice(..));
                rp.draw(0..PARTICLES, 0..1);
            }
            queue.submit([enc.finish()]);
            frame.present();
        }

        win.request_animation_frame(f.borrow().as_ref().unwrap().as_ref().unchecked_ref())
            .unwrap();
    }) as Box<dyn FnMut()>));

    set_status(&format!("{} particles · {} targets", PARTICLES, targets.len()));
    window
        .request_animation_frame(g.borrow().as_ref().unwrap().as_ref().unchecked_ref())
        .unwrap();
}
