// (v1.7) site.loadModel: a .glb from the site's media, read inside the
// sandboxed front end into arrays for WebGPU (node transforms applied,
// textures decoded), and the /media/assets/ serving it relies on.
import { test, expect, MAIN, UC, DEFAULT_REF, waitLive, frontendFrame, requireRotation } from "./support";

const CUBE = "/media/assets/models/test/cube.glb";

test("imported assets are served on both origins with their types", async ({ request }) => {
  for (const origin of [MAIN, UC]) {
    const res = await request.get(origin + CUBE);
    expect(res.status(), origin).toBe(200);
    expect(res.headers()["content-type"]).toBe("model/gltf-binary");
    expect(res.headers()["access-control-allow-origin"]).toBe("*");
  }
  // anything outside assets/ with a model extension isn't served
  expect((await request.get(`${UC}/media/models/test/cube.glb`)).status()).toBe(404);
});

test("site.loadModel reads a .glb: meshes, transforms, bounds, texture", async ({ page }) => {
  requireRotation(DEFAULT_REF);
  await page.goto("/");
  await waitLive(page);
  const frame = await frontendFrame(page);
  const m = await frame.evaluate(async (url) => {
    const model = await (window as any).site.loadModel(url);
    const mesh = model.meshes[0], tex = mesh.material.texture;
    return {
      meshes: model.meshes.length, triangles: model.triangles, verts: mesh.positions.length / 3,
      indices: mesh.indices.length, idxType: mesh.indices.constructor.name, uvs: mesh.uvs && mesh.uvs.length,
      min: model.bounds.min.map((v: number) => +v.toFixed(3)), max: model.bounds.max.map((v: number) => +v.toFixed(3)),
      center: model.bounds.center.map((v: number) => +v.toFixed(3)), radius: +model.bounds.radius.toFixed(3),
      normalLen: +Math.hypot(mesh.normals[0], mesh.normals[1], mesh.normals[2]).toFixed(3),
      tex: tex ? [tex.width, tex.height] : null, roughness: mesh.material.roughness, alpha: mesh.material.alphaMode,
    };
  }, CUBE);
  // a unit cube, translated +1 on x under a parent scaled by 2: x from 1 to 3, y and z from -1 to 1
  expect(m).toEqual({
    meshes: 1, triangles: 12, verts: 24, indices: 36, idxType: "Uint32Array", uvs: 48,
    min: [1, -1, -1], max: [3, 1, 1], center: [2, 0, 0], radius: 1.732, normalLen: 1,
    tex: [2, 2], roughness: 0.8, alpha: "OPAQUE",
  });
  // bad input is refused, not fetched
  const err = await frame.evaluate(() => (window as any).site.loadModel("https://evil.example/x.glb").then(() => "loaded", (e: Error) => e.message));
  expect(err).toContain("/media/");
});
