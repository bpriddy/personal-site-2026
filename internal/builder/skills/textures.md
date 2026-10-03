description: real surface textures (wood, stone, metal, fabric...) from Poly Haven, for CSS backgrounds or WebGPU materials

# Textures

## Find and import
1. `polyhaven_search_textures` with a material ("weathered wood", "terrazzo", "brushed steel"). Look at the thumbnails; choose for colour and scale of detail.
2. `polyhaven_import_texture` with the id and "1k" (use "2k" only for a surface seen large and close). You get /media/ paths for: color.jpg (sRGB), normal.jpg (OpenGL convention), rough.jpg, ao.jpg. All CC0.

## In CSS
`background: url(/media/assets/textures/polyhaven/<id>/1k/color.jpg) center / 512px repeat;` Blend it into the design (`background-blend-mode`, a tinted overlay, low contrast) so text on it stays readable; never put body text straight on a busy texture.

## In WebGPU
Load each map as an image (`img.crossOrigin = "anonymous"; img.src = path; await img.decode(); const bm = await createImageBitmap(img);`), then upload: color as `rgba8unorm-srgb`, the others as `rgba8unorm` (they are data, not colour). Use a repeating sampler (`addressModeU/V: "repeat"`, linear filtering, mipmaps if you can). A simple shading recipe:
```wgsl
let albedo = textureSample(colorTex, samp, uv * tiling).rgb;
let tn = textureSample(normalTex, samp, uv * tiling).xyz * 2.0 - 1.0;   // tangent space, +Y up
let rough = textureSample(roughTex, samp, uv * tiling).r;
let ao = textureSample(aoTex, samp, uv * tiling).r;
// with a flat surface facing +z you can use tn directly as the normal; otherwise build a TBN basis
let n = normalize(tn);
let diff = max(dot(n, L), 0.0);
let spec = pow(max(dot(reflect(-L, n), V), 0.0), mix(64.0, 4.0, rough)) * (1.0 - rough) * 0.5;
let col = albedo * (diff * 0.85 + 0.2) * ao + spec;
```
A raking light (low angle across the surface) shows texture best.
