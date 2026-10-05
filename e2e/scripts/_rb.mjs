import { chromium } from "@playwright/test";
const out = process.argv[2];
const fes = ["fe/a-liquid-glass-inspired-design-lots", "fe/a-mario-kart-style-environment-winding-6", "fe/a-set-of-webgpu-layers", "fe/a-star-wars-story-crawl-in-space", "fe/cubes-of-information"];
const b = await chromium.launch({ args: ["--enable-unsafe-webgpu", "--enable-features=Vulkan", "--use-angle=vulkan", "--ignore-gpu-blocklist"] });
for (const fe of fes) {
  const ctx = await b.newContext({ viewport: { width: 1440, height: 900 } });
  await ctx.addCookies([{ name: "fe_pick", value: fe, domain: "benpriddy.com", path: "/", secure: true }]);
  const p = await ctx.newPage(); const errs = [];
  p.on("pageerror", (e) => errs.push(e.message)); p.on("console", (m) => { if (m.type() === "error") errs.push(m.text().slice(0, 140)); });
  await p.goto("https://benpriddy.com/", { waitUntil: "load" }); await p.waitForTimeout(9000);
  const live = await p.evaluate(() => document.documentElement.classList.contains("fe-live"));
  const f = p.frames().find((x) => x.url().includes("usercontent"));
  const r = f ? await f.evaluate(() => {
    const all = [...document.querySelectorAll("body *")].filter((e) => e.children.length === 0 && /Global Head of AI Technology/.test(e.textContent));
    const el = all[0]; if (el) el.scrollIntoView({ block: "center" });
    const vis = el ? (() => { const s = getComputedStyle(el), r = el.getBoundingClientRect(); return s.visibility !== "hidden" && s.display !== "none" && +s.opacity > 0.1 && r.width > 0; })() : false;
    return { domHasRole: !!el, visible: vis, hasTool: /Tool of North America/.test(document.body.innerText), hasWork: /QLEDecode/.test(document.body.innerText) };
  }) : "no frame";
  await p.waitForTimeout(1500);
  await p.screenshot({ path: `${out}_${fe.split("/")[1].slice(0, 20)}.png` });
  console.log(fe.padEnd(46), "live:", live, JSON.stringify(r), errs.slice(0, 3).join(" || ") || "no errors");
  await ctx.close();
}
await b.close();
