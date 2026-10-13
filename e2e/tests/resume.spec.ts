// /resume (v1.14, v1.15): one fixed page outside the rotation. The career as
// a space you travel through (drawn by resume.js from the page's JSON), the
// plain resume under it, and a PDF made from the same data.
import { test, expect } from "./support";

test("/resume builds the space, with Ben's photo at the entrance", async ({ page, request }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
  await page.goto("/resume");
  await expect(page.locator("h1.rz-name")).not.toBeEmpty();
  // no front end here: it's the same page for everyone
  await expect(page.locator("iframe")).toHaveCount(0);
  await expect(page.locator("#rz-system")).toHaveClass(/rz-3d/);
  await expect(page.locator(".rz-canvas")).toHaveCount(1);
  const nodes = page.locator(".rz-node");
  await expect.poll(() => nodes.count()).toBeGreaterThan(1);
  // the skills are nodes of their own, in two constellations
  await expect(page.locator('.rz-node[data-hub][data-group="do"]')).toHaveCount(1);
  await expect(page.locator('.rz-node[data-hub][data-group="review"]')).toHaveCount(1);
  expect(await page.locator('.rz-node[data-kind="skill"]:not([data-hub])').count()).toBeGreaterThan(2);
  // the photo, in the centre node, loaded
  const photo = page.locator('.rz-node[data-kind="me"] img');
  await expect(photo).toHaveAttribute("src", /\/static\/resume\/ben-480\.jpg$/);
  await expect.poll(() => photo.evaluate((i: HTMLImageElement) => i.complete && i.naturalWidth)).toBeGreaterThan(0);
  await page.waitForTimeout(1800); // the way in settles
  await expect.poll(() => page.locator('.rz-node[data-kind="me"]').evaluate((e) => Number(getComputedStyle(e).opacity))).toBeGreaterThan(0.5);
  // the plain resume is there for readers and search
  await expect(page.locator(".rz-doc")).toContainText("Profile");
  await expect(page.locator(".rz-doc")).toContainText("What I can review");
  const pdf = await request.get("/resume.pdf");
  expect(pdf.status()).toBe(200);
  expect(pdf.headers()["content-type"]).toBe("application/pdf");
  expect(errors).toEqual([]);
});

test("/resume: scrolling travels through the space", async ({ page }) => {
  await page.goto("/resume");
  const stage = page.locator("#rz-stage");
  await expect.poll(() => stage.getAttribute("data-depth")).not.toBeNull();
  await page.waitForTimeout(1800);
  const depth = async () => Number(await stage.getAttribute("data-depth"));
  const before = await depth();
  // halfway through the section's scroll
  await page.evaluate(() => {
    const t = document.getElementById("rz-travel")!;
    window.scrollTo(0, t.getBoundingClientRect().top + window.scrollY + 0.5 * (t.offsetHeight - window.innerHeight));
  });
  await expect.poll(depth, { timeout: 5000 }).toBeGreaterThan(before + 200);
  // the stage stays on screen while the page scrolls by
  const box = (await stage.boundingBox())!;
  expect(box.y).toBeLessThanOrEqual(1);
});

test("/resume: a node opens its panel and traces its links", async ({ page }) => {
  await page.goto("/resume");
  await page.waitForTimeout(2000); // the way in settles
  const me = page.locator('.rz-node[data-kind="me"] .rz-d');
  const box = (await me.boundingBox())!;
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  const panel = page.locator("#rz-panel");
  await expect(panel).toBeVisible();
  await expect(panel.locator(".rz-panel-kind")).toHaveText("Profile");
  await expect(page.locator('.rz-node[data-kind="me"]')).toHaveClass(/rz-focus/);
  // its connections light up (Ben links to his roles and both constellations)
  expect(await page.locator(".rz-node.rz-on").count()).toBeGreaterThan(2);
  // its neighbours are in the panel; one opens in turn
  await panel.locator(".rz-panel-links button", { hasText: "What I can review" }).click();
  await expect(panel.locator(".rz-panel-title")).toHaveText("What I can review");
  await page.keyboard.press("Escape");
  await expect(panel).toBeHidden();
});

test("/resume: reduced motion is a still composition", async ({ browser }) => {
  const ctx = await browser.newContext({ reducedMotion: "reduce" });
  const page = await ctx.newPage();
  await page.goto("/resume");
  await expect(page.locator("#rz-system")).toHaveClass(/rz-still/);
  // no scroll-driven travel: the section is just the stage
  const h = await page.locator("#rz-travel").evaluate((e) => e.offsetHeight);
  expect(h).toBeLessThan(1200);
  // choosing something cuts straight to it and opens it
  await page.locator('.rz-node[data-hub][data-group="review"]').evaluate((b: HTMLElement) => b.click());
  await expect(page.locator("#rz-panel .rz-panel-title")).toHaveText("What I can review");
  // a year (when the CMS has roles) cuts to it too
  const years = page.locator(".rz-rail button");
  if (await years.count()) {
    await years.last().click();
    await expect(years.last()).toHaveClass(/rz-here/);
  }
  await ctx.close();
});
