description: real 3D models from Sketchfab, loaded with site.loadModel and drawn in WebGPU (search, choose, import, render, credit)

# 3D models

## Find and import
1. `sketchfab_search` with a plain noun ("x-wing", "vintage microphone", "low poly pine tree"). Look at the thumbnails: choose the one whose look fits the concept (style, detail, colour), then the lighter one (fewer triangles, fewer textures) when two are close. Stylised or low-poly models often suit a designed page better than photoscans.
2. `sketchfab_import` the uid. You get its `/media/...glb` path, its contents, and (for CC BY / CC BY-SA) a credit line you must show as visible text wherever it appears.
3. Up to 4 imports per run. Reuse one model many times (instances) rather than importing many.

## Load
```js
const model = await site.loadModel("/media/assets/models/sketchfab/<uid>.glb");
// model.meshes: [{ positions: Float32Array (xyz, node transforms already applied),
//                  normals: Float32Array (xyz), uvs: Float32Array | null (uv),
//                  indices: Uint32Array,
//                  material: { baseColor: [r,g,b,a] (linear), texture: ImageBitmap | null (base colour, sRGB),
//                              metallic, roughness, emissive: [r,g,b], alphaMode: "OPAQUE"|"MASK"|"BLEND", doubleSided } }]
// model.bounds: { min: [x,y,z], max: [x,y,z], center: [x,y,z], radius }
// model.triangles: number
```
It rejects (throws) on a network or parse error: catch it, report with site.reportError(err), and keep the page working without the model. Load models after site.ready() (content first). Animations aren't played (rest pose).

## Draw (WebGPU)
Normalise with the bounds: translate by `-center`, scale by `1 / radius`, so every model is a unit sphere you can place and size by design.

One pipeline serves every mesh: a vertex buffer per mesh (positions, normals, uvs interleaved or separate), a uniform per draw (model-view-projection, model matrix, base colour, flags), and the texture (or a 1×1 white one when `texture` is null). Upload a texture with:
```js
const tex = device.createTexture({ size: [bm.width, bm.height], format: "rgba8unorm-srgb",
  usage: GPUTextureUsage.TEXTURE_BINDING | GPUTextureUsage.COPY_DST | GPUTextureUsage.RENDER_ATTACHMENT });
device.queue.copyExternalImageToTexture({ source: bm }, { texture: tex }, [bm.width, bm.height]);
```
A shader that reads as designed rather than default (a key, a soft fill, a rim; matte by default):
```wgsl
struct U { mvp: mat4x4f, model: mat4x4f, color: vec4f, light: vec4f, flags: vec4f };
@group(0) @binding(0) var<uniform> u: U;
@group(0) @binding(1) var t: texture_2d<f32>;
@group(0) @binding(2) var s: sampler;
struct VO { @builtin(position) p: vec4f, @location(0) n: vec3f, @location(1) uv: vec2f };
@vertex fn vs(@location(0) pos: vec3f, @location(1) nor: vec3f, @location(2) uv: vec2f) -> VO {
  var o: VO; o.p = u.mvp * vec4f(pos, 1.0); o.n = normalize((u.model * vec4f(nor, 0.0)).xyz); o.uv = uv; return o;
}
@fragment fn fs(i: VO) -> @location(0) vec4f {
  let base = textureSample(t, s, i.uv) * u.color;
  if (u.flags.x > 0.5 && base.a < 0.5) { discard; }              // alphaMode MASK
  let n = normalize(i.n);
  let key = normalize(u.light.xyz);
  let k = max(dot(n, key), 0.0);                                 // key light
  let fill = 0.25 + 0.15 * n.y;                                  // soft sky fill
  let rim = pow(1.0 - abs(n.z), 3.0) * 0.35;                     // rim, view along -z
  return vec4f(base.rgb * (k * 0.9 + fill) + rim * u.light.w, base.a);
}
```
Use a depth buffer (`depth24plus`), back-face culling unless `doubleSided`, and draw BLEND materials last. For a film or print look, grade in the shader (lift blacks, warm highlights, grain) rather than chasing realism.

## Stage it
Follow the quality bar's 3D rules: a composed, offset camera; the model as subject or as a deliberate element of the composition, never floating dead-centre on black. Cap the canvas resolution, pause when hidden, and keep a static, still-good frame under prefers-reduced-motion. The DOM content stays the readable content.

## Credit
Show the import's credit line as text (e.g. a small "Model: “X-Wing” by Name, CC BY 4.0" line in a corner, footer or credits route) on every route where the model appears. finish refuses to save without it.
