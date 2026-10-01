// (e) Runs only in the no-webgpu project (Chromium with --disable-webgpu).
//
// The default front end (builtin/site) is DOM-first: its content is HTML in
// the house fonts and WebGPU only adds a field behind it, so without WebGPU
// it still goes live and shows the content. When no front end can run at all
// (here: the user-content origin is unreachable), the parent removes the
// iframe and the transcript is the site.
import { test, expect, UC, DEFAULT_REF, SETTLE_TIMEOUT, requireRotation, waitLive, frontendFrame, decodeToken, tokenFromURL } from "./support";

test("without WebGPU the default front end still shows the content", async ({ page, parentErrors }) => {
  requireRotation(DEFAULT_REF);
  const reports: string[] = [];
  await page.route("**/api/observe", (route) => {
    reports.push(route.request().postData() ?? "");
    return route.fulfill({ status: 204 });
  });
  await page.goto("/");
  await waitLive(page, SETTLE_TIMEOUT);
  const src = (await page.locator("iframe").getAttribute("src"))!;
  expect(decodeToken(tokenFromURL(src)).r).toBe(DEFAULT_REF);

  const frame = await frontendFrame(page);
  expect(await frame.evaluate(async () => { const gpu = (navigator as any).gpu; return !gpu || (await gpu.requestAdapter()) === null; })).toBe(true);
  // the name, the bio and the nav are real text, visible in the front end
  await expect(frame.locator("h1.name")).toHaveText("Ben Priddy");
  await expect(frame.locator("h1.name")).toBeVisible();
  await expect(frame.locator(".site-header nav a")).not.toHaveCount(0);
  // in the house fonts, loaded from /fonts/ on the user-content origin under the sandbox CSP
  expect(await frame.evaluate(() => document.fonts.ready.then(() => document.fonts.check("300 100px Newsreader")))).toBe(true);
  expect(await frame.evaluate(() => Array.from(document.fonts as any as Iterable<FontFace>).filter((f) => f.status === "error").map((f) => f.family))).toEqual([]);
  // no WebGPU field, and no error for its absence
  await expect(frame.locator("#field")).not.toHaveClass(/(^|\s)on(\s|$)/);
  await page.waitForTimeout(1_000);
  await expect(page.locator("html")).toHaveClass(/fe-live/);
  expect(reports.filter((r) => r.includes('"frontend-error"'))).toEqual([]);
  expect(await parentErrors()).toEqual([]);
});

test("when no front end can run, the transcript is the site", async ({ page, parentErrors }) => {
  await page.route(`${UC}/**`, (route) => route.abort());
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
  // set in the house fonts
  expect(await h1.evaluate((el) => getComputedStyle(el).fontFamily)).toMatch(/^"?Newsreader/);
  expect(await page.evaluate(() => document.fonts.ready.then(() => document.fonts.check("300 100px Newsreader")))).toBe(true);

  // and it stays that way
  await page.waitForTimeout(1_000);
  await expect(page.locator("iframe")).toHaveCount(0);
  expect(await parentErrors()).toEqual([]);
});
