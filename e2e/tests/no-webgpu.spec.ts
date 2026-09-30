// (e) Runs only in the no-webgpu project (Chromium with --disable-webgpu):
// every front end fails, so the parent removes the iframe and the transcript
// is the site.
import { test, expect, SETTLE_TIMEOUT } from "./support";

test("without WebGPU the transcript is the site", async ({ page, parentErrors }) => {
  const firstPick = page.waitForResponse((r) => new URL(r.url()).pathname === "/api/frontend", { timeout: 15_000 });
  await page.goto("/");
  await firstPick;

  // fe-loading is set before the fetch, so "no classes and no iframe" is only
  // reachable by giving up
  await expect(page.locator("iframe")).toHaveCount(0, { timeout: SETTLE_TIMEOUT });
  const html = page.locator("html");
  await expect(html).not.toHaveClass(/fe-(loading|live)/);

  const h1 = page.locator("#transcript main h1");
  await expect(h1).toHaveText("Ben Priddy");
  await expect(h1).toBeVisible();
  await expect(h1).toBeInViewport();
  // not the 1px visually-hidden clip
  const box = await page.locator("#transcript main").boundingBox();
  expect(box!.width).toBeGreaterThan(100);
  expect(box!.height).toBeGreaterThan(20);
  const clipped = await page.locator("#transcript").evaluate((el) => {
    const s = getComputedStyle(el);
    return { clip: s.clip, clipPath: s.clipPath, overflow: s.overflow, height: el.getBoundingClientRect().height };
  });
  expect(clipped.clipPath).toBe("none");
  expect(clipped.clip).toMatch(/^(auto|)$/);
  expect(clipped.height).toBeGreaterThan(20);

  // and it stays that way
  await page.waitForTimeout(1_000);
  await expect(page.locator("iframe")).toHaveCount(0);
  expect(await parentErrors()).toEqual([]);
});
