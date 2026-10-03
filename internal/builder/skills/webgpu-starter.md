description: a robust WebGPU setup that never blocks the content (device, canvas, resize, DPR cap, loss, reduced motion, pausing)

# WebGPU starter

Render the DOM content and call site.ready() first; start this afterwards.

```js
async function startGPU(canvas, frame) {
  if (!navigator.gpu) return null;                       // no WebGPU: the DOM page is the page
  let adapter, device;
  try {
    adapter = await navigator.gpu.requestAdapter();
    device = adapter && await adapter.requestDevice();
  } catch (e) { return null; }
  if (!device) return null;
  device.lost.then((info) => { if (info.reason !== "destroyed") { stop(); site.reportError(info.message || "device lost"); } });
  const ctx = canvas.getContext("webgpu");
  const format = navigator.gpu.getPreferredCanvasFormat();
  ctx.configure({ device, format, alphaMode: "premultiplied" });
  const reduce = matchMedia("(prefers-reduced-motion: reduce)");
  let raf = 0, visible = true, running = true;
  function size() {
    const dpr = Math.min(devicePixelRatio || 1, 2);
    const w = Math.max(1, Math.round(canvas.clientWidth * dpr)), h = Math.max(1, Math.round(canvas.clientHeight * dpr));
    // cap the pixel count: big screens don't need 4K buffers
    const s = Math.min(1, Math.sqrt(3.2e6 / (w * h)));
    const W = Math.round(w * s), H = Math.round(h * s);
    if (canvas.width !== W || canvas.height !== H) { canvas.width = W; canvas.height = H; return true; }
    return false;
  }
  function loop(t) {
    raf = 0;
    if (!running || !visible || document.hidden) return;
    size();
    try { frame(device, ctx, t / 1000); } catch (e) { stop(); site.reportError(e); return; }
    if (!reduce.matches) raf = requestAnimationFrame(loop);   // reduced motion: one still frame per change
  }
  function kick() { if (!raf && running) raf = requestAnimationFrame(loop); }
  function stop() { running = false; if (raf) cancelAnimationFrame(raf); }
  new ResizeObserver(kick).observe(canvas);
  new IntersectionObserver((e) => { visible = e[0].isIntersecting; kick(); }).observe(canvas);
  document.addEventListener("visibilitychange", kick);
  reduce.addEventListener("change", kick);
  kick();
  return { device, format, kick, stop };
}
```

Rules that keep it fast and alive over time:
- Create pipelines, buffers, bind groups and texture views once (or when the size changes), not per frame. Reuse typed arrays for uniforms.
- Only `getCurrentTexture().createView()` and the command encoder are per frame.
- When nothing is moving, stop requesting frames (or drop to 30fps) and restart on input.
- MSAA ×4 is fine at the capped size; skip it on phones if the scene is heavy.
- Images drawn into textures need `crossOrigin = "anonymous"` (the frame's origin is opaque).
