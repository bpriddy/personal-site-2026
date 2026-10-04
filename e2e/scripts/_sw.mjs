import { chromium } from "@playwright/test";
const out = process.argv[2];
const b = await chromium.launch({ args: ["--enable-unsafe-webgpu", "--enable-features=Vulkan", "--use-angle=vulkan", "--ignore-gpu-blocklist"] });
for (const [tag, opts, n] of [["d", { viewport: { width: 1440, height: 900 } }, 14], ["m", { viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, hasTouch: true, isMobile: true }, 10]]) {
  const ctx = await b.newContext(opts);
  await ctx.addCookies([{ name: "fe_pick", value: "fe/a-star-wars-story-crawl-in-space", domain: "benpriddy.com", path: "/", secure: true }]);
  const p = await ctx.newPage(); const errs = [];
  p.on("pageerror", (e) => errs.push("pageerror " + e.message)); p.on("console", (m) => { if (m.type() === "error" || m.type() === "warning") errs.push(m.type() + " " + m.text().slice(0, 160)); });
  const reqs = []; p.on("response", (r) => { if (r.url().includes(".glb")) reqs.push(r.status() + " " + r.url().split("/").pop()); });
  await p.goto("https://benpriddy.com/", { waitUntil: "load" }); await p.waitForTimeout(6000);
  for (let i = 0; i < n; i++) { await p.screenshot({ path: `${out}_${tag}_${String(i).padStart(2, "0")}.png` }); await p.waitForTimeout(3500); }
  console.log(tag, "glb:", reqs.join(", ") || "none", "|", errs.slice(0, 6).join(" || ") || "no errors");
  await ctx.close();
}
await b.close();
