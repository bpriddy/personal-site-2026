// builtin/stream, the site version of Particle Stream: baked content, paged
// screens, and every screen published to the wasm as an obstacle scene.
import { test, expect, requireRotation, waitLive, frontendFrame } from "./support";

test.describe("builtin/stream", () => {
  test.beforeEach(() => requireRotation("builtin/stream"));

  test("renders baked content and pages through it", async ({ page, parentErrors }) => {
    await page.goto("/");
    await waitLive(page);
    const f = await frontendFrame(page);

    // the wasm drew the scene: the GPU's relief type replaces the DOM's
    await f.waitForFunction(() => (window as any).__SCENE_DRAWN > 0, null, { timeout: 20_000 });
    await expect(f.locator("html")).toHaveClass(/gpu-type/);
    await expect(f.locator(".st-screen.is-on h1")).not.toBeEmpty();
    const scene = await f.evaluate(() => (window as any).__SCENE);
    expect(scene.page.glyphs.length).toBeGreaterThan(0);
    expect(scene.chrome.glyphs.length).toBeGreaterThanOrEqual(3); // the menu, in relief
    expect(scene.chrome.plates.length).toBeGreaterThanOrEqual(1); // the pager

    // baked: the front end fetched no content of its own
    const fetched = await f.evaluate(() => performance.getEntriesByType("resource").map((e) => e.name));
    expect(fetched.filter((u) => /\/api\/|site\.json/.test(u))).toEqual([]);

    // paging: a key steps to the next screen, which is a new scene
    const pager = f.locator(".st-pager-n");
    await expect(pager).toHaveText(/^01 \/ \d\d$/);
    await f.locator(".st-screen.is-on h1").click(); // keys go to the frame once it has focus
    await page.keyboard.press("ArrowDown");
    await expect(pager).toHaveText(/^02 \/ \d\d$/);
    await f.waitForFunction((g) => (window as any).__SCENE.gen > g, scene.gen);
    await expect(f.locator(".st-screen.is-on .st-panel").first()).toBeVisible();
    await f.locator(".st-pager-b", { hasText: "Back" }).click();
    await expect(pager).toHaveText(/^01 \/ \d\d$/);

    // the nav routes through the site
    await f.locator(".st-nav-link", { hasText: "Work" }).click();
    await expect(page).toHaveURL(/\/work$/);
    await expect(f.locator(".st-screen.is-on h1")).toHaveText("WORK");
    expect(await parentErrors()).toEqual([]);
  });
});
