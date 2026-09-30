import { chromium, request } from "@playwright/test";
const out = process.argv[2];
const b = await chromium.launch({ channel: "chromium-headless-shell",
  args: ["--enable-unsafe-webgpu","--enable-features=Vulkan","--use-angle=vulkan","--ignore-gpu-blocklist"] });
for (const ref of ["builtin/site", "builtin/particle-stream"]) {
  const ctx = await b.newContext({ viewport: { width: 1280, height: 800 } });
  await ctx.addCookies([{ name: "fe_pick", value: ref, url: "https://benpriddy.com" }]);
  const p = await ctx.newPage();
  const errs = []; p.on("pageerror", e => errs.push(String(e)));
  await p.goto("https://benpriddy.com/");
  const live = await p.waitForFunction(() => document.documentElement.classList.contains("fe-live"), null, { timeout: 30000 }).then(() => true, () => false);
  const src = await p.locator("iframe").getAttribute("src").catch(() => null);
  console.log(`${ref}: fe-live=${live} iframe=${src && new URL(src).host} sandbox=${await p.locator("iframe").getAttribute("sandbox").catch(()=>null)} parentErrors=${errs.length}`);
  await p.screenshot({ path: `${out}/prod-${ref.replace("/", "-")}.png` });
  if (ref === "builtin/site") {
    const api = await request.newContext();
    const r = await api.get(src); console.log(`  direct open of iframe URL: ${r.status()}`);
    const w = await api.get("https://www.benpriddy.com/about", { maxRedirects: 0 }); console.log(`  www: ${w.status()} → ${w.headers()["location"]}`);
    const a = await api.get("https://benpriddy.com/admin/"); console.log(`  admin without auth: ${a.status()}`);
  }
  await ctx.close();
}
await b.close();
