// (c) Navigation requested from inside the iframe: the parent owns history,
// swaps the transcript, and tells the front end the new route.
import { test, expect, savePage, waitLive, frontendFrame } from "./support";

test("site.navigate from the iframe updates URL, transcript and route", async ({ page, request, parentErrors }) => {
  await savePage(request, { slug: "about", title: "About", body: "About page created by e2e." });

  await page.goto("/");
  await waitLive(page);
  const frame = await frontendFrame(page);
  await frame.waitForFunction(() => (window as any).site.route === "");

  await frame.evaluate(() => (window as any).site.navigate("about"));

  await expect(page).toHaveURL(/\/about$/);
  await expect(page.locator("#transcript main h1")).toHaveText("About");
  await expect(page).toHaveTitle(/About/);
  await frame.waitForFunction(() => (window as any).site.route === "about");

  // popstate does the same without pushing
  await page.goBack();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.locator("#transcript main h1")).toHaveText("Ben Priddy");
  await frame.waitForFunction(() => (window as any).site.route === "");

  // same iframe throughout: navigation doesn't reload the front end
  expect(page.frames()).toContain(frame);
  await expect(page.locator("html")).toHaveClass(/fe-live/);
  expect(await parentErrors()).toEqual([]);
});

test("site.onRoute subscribers see the new route", async ({ page, request }) => {
  await savePage(request, { slug: "about", title: "About", body: "About page created by e2e." });
  await page.goto("/");
  await waitLive(page);
  const frame = await frontendFrame(page);
  await frame.evaluate(() => {
    const w = window as any;
    w.__routes = [];
    w.site.onRoute((r: string) => w.__routes.push(r));
    w.site.navigate("about");
  });
  await frame.waitForFunction(() => (window as any).__routes.includes("about"));
});
