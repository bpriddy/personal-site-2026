// Tries Chromium launch-flag combinations against fixtures/webgpu-probe.html and
// prints what each one yields. Diagnostic only; the flags that win are recorded
// in playwright.config.ts. Usage: node scripts/probe-flags.mjs
import { chromium } from "@playwright/test";
import { readFile } from "node:fs/promises";
import http from "node:http";

const page = await readFile(new URL("../fixtures/webgpu-probe.html", import.meta.url));
const server = http.createServer((_, res) => { res.setHeader("content-type", "text/html"); res.end(page); });
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const url = `http://127.0.0.1:${server.address().port}/`;

const combos = {
  "gpu-vulkan": ["--enable-unsafe-webgpu", "--enable-features=Vulkan", "--use-angle=vulkan", "--ignore-gpu-blocklist"],
  "gpu-vulkan-no-blocklist-flag": ["--enable-unsafe-webgpu", "--enable-features=Vulkan", "--use-angle=vulkan"],
  "gpu-default": ["--enable-unsafe-webgpu", "--ignore-gpu-blocklist"],
  "swiftshader": ["--enable-unsafe-webgpu", "--enable-unsafe-swiftshader", "--use-webgpu-adapter=swiftshader"],
  "swiftshader+angle": ["--enable-unsafe-webgpu", "--enable-unsafe-swiftshader", "--use-webgpu-adapter=swiftshader", "--use-angle=swiftshader"],
  "swiftshader-vulkan": ["--enable-unsafe-webgpu", "--enable-unsafe-swiftshader", "--enable-features=Vulkan", "--use-vulkan=swiftshader", "--use-webgpu-adapter=swiftshader", "--use-angle=swiftshader"],
  "swiftshader-disable-gpu": ["--enable-unsafe-webgpu", "--enable-unsafe-swiftshader", "--disable-gpu", "--use-webgpu-adapter=swiftshader"],
  "disabled": ["--disable-webgpu"],
  "disabled-blink": ["--disable-blink-features=WebGPU"],
  "disabled-feature": ["--disable-features=WebGPU"],
  "none": [],
};
const only = process.argv.slice(2);
for (const shell of [true, false]) {
  for (const [name, args] of Object.entries(combos)) {
    if (only.length && !only.includes(name)) continue;
    let res;
    let browser;
    try {
      browser = await chromium.launch({ headless: true, channel: shell ? undefined : "chromium", args });
      const p = await browser.newPage();
      await p.goto(url);
      res = await p.waitForFunction(() => window.__probe, null, { timeout: 20000 }).then((h) => h.jsonValue());
    } catch (e) {
      res = { launchError: String(e).split("\n")[0] };
    } finally {
      await browser?.close().catch(() => {});
    }
    console.log(`${shell ? "headless-shell" : "chromium(new headless)"} ${name}: ${JSON.stringify(res)}`);
  }
}
server.close();
