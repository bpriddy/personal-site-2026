// (v1.4) Projects: /work and /work/<slug> in the transcript and in the
// default front end, their media (loops that actually load), the host API's
// project accessors, and site.openExternal (the parent opens http(s) only).
import { readFileSync } from "node:fs";
import type { APIRequestContext, BrowserContext, Page } from "@playwright/test";
import { test, expect, MAIN, DEFAULT_REF, adminAuth, waitLive, frontendFrame, requireRotation } from "./support";

const fixture = JSON.parse(readFileSync(new URL("../fixtures/e2e-projects.json", import.meta.url), "utf8"));

async function importProjects(request: APIRequestContext) {
  const res = await request.post(`${MAIN}/admin/import/projects`, { headers: adminAuth, data: fixture });
  expect(res.status(), await res.text()).toBe(200);
  return res.json();
}

/** Shows the transcript: no front end can load. */
async function transcriptOnly(page: Page) {
  await page.route("**/api/frontend*", (r) => r.fulfill({ status: 503, body: "no front end in this test" }));
}

/** Answers every external URL locally, so tests never leave the machine. */
async function stubExternal(context: BrowserContext) {
  await context.route(/^https:\/\/(example\.com|www\.youtube\.com|www\.youtube-nocookie\.com)\//, (r) =>
    r.fulfill({ status: 200, contentType: "text/html", body: "<!doctype html><title>external</title>" }),
  );
}

/** Waits until the video has decoded data (it played, or at least loaded a frame). */
const loaded = (v: HTMLVideoElement) => v.readyState >= 2 && !!v.currentSrc;

test.beforeEach(async ({ request }) => {
  await importProjects(request);
});

test("the import is idempotent and never touches pages", async ({ request }) => {
  const before = await (await request.get("/api/site.json")).json();
  const again = await importProjects(request);
  expect(again.projects.created).toEqual([]);
  expect(again.projects.updated).toEqual([]);
  expect(again.projects.unchanged).toEqual(["e2e-work", "e2e-second", "e2e-draft"]);
  const after = await (await request.get("/api/site.json")).json();
  expect(after.pages).toEqual(before.pages);
  const p = after.projects.find((x: any) => x.slug === "e2e-work");
  expect(p.media[1]).toEqual({
    kind: "loop", src: "/media/projects/e2e-work/loop-1.mp4", poster: "/media/projects/e2e-work/loop-1.jpg", width: 160, height: 40, alt: "",
  });
  expect(after.projects.find((x: any) => x.slug === "e2e-draft")).toBeUndefined();
});

test.describe("transcript", () => {
  test("/work lists the published projects, numbered, with their loops", async ({ page }) => {
    await transcriptOnly(page);
    await page.goto("/work/");
    const main = page.locator("#transcript main");
    await expect(main.locator("h1")).toHaveText("Work");
    const row = main.locator(".work-row", { hasText: "E2E Work" });
    await expect(row.locator(".work-meta")).toHaveText("Test Client · 2020");
    await expect(row.locator("a")).toHaveAttribute("href", "/work/e2e-work");
    await expect(main.locator(".work-row", { hasText: "E2E Draft" })).toHaveCount(0);
    await expect(page.locator('#transcript nav a[aria-current="page"]')).toContainText("Work");
    const video = row.locator("video");
    await expect(video).toHaveAttribute("poster", "/media/projects/e2e-work/loop-1.jpg");
    // hover plays the loop (pointer devices)
    await row.hover();
    await expect.poll(() => video.evaluate(loaded), { timeout: 10_000 }).toBe(true);
  });

  test("/work/<slug> renders the project and its loop loads", async ({ page, context }) => {
    await stubExternal(context);
    await transcriptOnly(page);
    const res = await page.goto("/work/e2e-work");
    expect(res?.status()).toBe(200);
    const main = page.locator("#transcript main");
    await expect(main.locator("h1")).toHaveText("E2E Work");
    await expect(main.locator(".project-meta")).toContainText("Test Agency");
    await expect(main.locator(".project-meta")).toContainText("Experiential, Test");
    await expect(main.locator(".project-hero img")).toHaveAttribute("src", "/media/projects/e2e-work/hero.jpg");
    await expect(main.locator(".project-text").first()).toContainText("Its second paragraph.");
    await expect(main.locator('a[href="https://example.com/e2e-work"]')).toHaveAttribute("rel", "noopener noreferrer");
    const video = main.locator(".project-loops video").first();
    await video.scrollIntoViewIfNeeded();
    await expect.poll(() => video.evaluate(loaded), { timeout: 10_000 }).toBe(true);
    await expect.poll(() => video.evaluate((v: HTMLVideoElement) => !v.paused)).toBe(true);
    // the film is a facade until asked for
    await expect(main.locator(".film iframe")).toHaveCount(0);
    await main.locator(".film-play").click();
    await expect(main.locator(".film iframe")).toHaveAttribute("src", /^https:\/\/www\.youtube-nocookie\.com\/embed\/e6PKBbvRYV0/);
    await expect(main.locator(".project-next .next-title")).toHaveText("E2E Second");
  });

  test("reduced motion: loops stay posters", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await transcriptOnly(page);
    await page.goto("/work/e2e-work");
    const video = page.locator("#transcript .project-loops video").first();
    await video.scrollIntoViewIfNeeded();
    await page.waitForTimeout(800);
    expect(await video.evaluate((v: HTMLVideoElement) => v.paused)).toBe(true);
  });

  test("unpublished and unknown projects are 404", async ({ request }) => {
    for (const p of ["/work/e2e-draft", "/work/no-such-project"]) {
      const res = await request.get(p);
      expect(res.status(), p).toBe(404);
      expect(await res.text()).toContain("Not found.");
    }
  });

  test("media is served with the right headers by both origins", async ({ request }) => {
    const uc = process.env.E2E_USERCONTENT_ORIGIN ?? "http://127.0.0.1:8091";
    for (const origin of [MAIN, uc]) {
      const res = await request.get(`${origin}/media/projects/e2e-work/loop-1.mp4`, { headers: { Range: "bytes=0-9" } });
      expect(res.status(), origin).toBe(206);
      expect(res.headers()["content-type"]).toBe("video/mp4");
      expect(res.headers()["access-control-allow-origin"]).toBe("*");
      expect(res.headers()["cache-control"]).toContain("immutable");
      expect((await res.body()).length).toBe(10);
      expect((await request.get(`${origin}/media/projects/e2e-work/.hidden.jpg`)).status()).toBe(404);
    }
  });
});

test.describe("default front end", () => {
  test.beforeEach(() => requireRotation(DEFAULT_REF));

  test("renders /work and /work/<slug>; loops load; links open in a new tab", async ({ page, context, parentErrors }) => {
    await stubExternal(context);
    await page.goto("/work/");
    await waitLive(page);
    const frame = await frontendFrame(page);
    await expect(frame.locator("h1.display")).toHaveText("Work");
    const row = frame.locator(".work-row", { hasText: "E2E Work" });
    await expect(row.locator(".work-meta")).toHaveText("Test Client · 2020");
    await expect(frame.locator(".work-row", { hasText: "E2E Draft" })).toHaveCount(0);
    await expect(frame.locator('.site-header nav a[aria-current="page"]')).toContainText("Work");

    // into the project: the parent owns the URL
    await row.locator("a").click();
    await expect(page).toHaveURL(/\/work\/e2e-work$/);
    await expect(frame.locator("h1.display")).toHaveText("E2E Work");
    await expect(page.locator("#transcript main h1")).toHaveText("E2E Work");
    await expect(frame.locator(".project-meta")).toContainText("Test Agency");
    await expect(frame.locator(".project-hero img")).toHaveAttribute("src", "/media/projects/e2e-work/hero.jpg");
    const video = frame.locator(".project-loops video").first();
    await video.scrollIntoViewIfNeeded();
    await expect.poll(() => video.evaluate(loaded), { timeout: 10_000 }).toBe(true);
    await expect(video).toHaveAttribute("poster", "/media/projects/e2e-work/loop-1.jpg");

    // a link out: the front end asks, the parent opens a tab (noopener)
    const popup = context.waitForEvent("page");
    await frame.locator('a[data-external="https://example.com/e2e-work"]').click();
    const tab = await popup;
    await expect.poll(() => tab.url()).toBe("https://example.com/e2e-work");
    expect(await tab.evaluate(() => window.opener)).toBeNull();
    await tab.close();

    // the film opens on YouTube
    await page.waitForTimeout(1100); // the parent opens at most one tab a second
    const film = context.waitForEvent("page");
    await frame.locator('a[data-external^="https://www.youtube.com/watch?v=e6PKBbvRYV0"]').click();
    await expect.poll(async () => (await film).url()).toBe("https://www.youtube.com/watch?v=e6PKBbvRYV0");
    expect(await parentErrors()).toEqual([]);
  });

  test("home features the selected work after the bio", async ({ page }) => {
    await page.goto("/");
    await waitLive(page);
    const frame = await frontendFrame(page);
    const work = frame.locator(".work-index");
    await expect(work.locator(".label").first()).toContainText("Selected work");
    await expect(work.locator(".work-row", { hasText: "E2E Work" })).toHaveCount(1);
  });

  test("host API: projects accessors and openExternal", async ({ page, context }) => {
    await stubExternal(context);
    await page.goto("/");
    await waitLive(page);
    const frame = await frontendFrame(page);
    const r = await frame.evaluate(() => {
      const site = (window as any).site;
      const p = site.project("e2e-work");
      return {
        count: site.projects().filter((x: any) => x.slug.startsWith("e2e-")).length,
        same: site.collection("projects").length === site.projects().length,
        unknownCollection: site.collection("nope"),
        missing: site.project("nope"),
        tagged: p._collection,
        tags: site.field(p, "tags", { expect: "list" }),
        media: site.field(p, "media", { expect: "list" }).map((m: any) => Object.keys(m).sort().join(",")),
        refused: ["javascript:alert(1)", "data:text/html,hi", "/work/e2e-work", "//evil.example/x", "", null, 42].map((u) => site.openExternal(u)),
      };
    });
    expect(r.count).toBe(2);
    expect(r.same).toBe(true);
    expect(r.unknownCollection).toEqual([]);
    expect(r.missing).toEqual({});
    expect(r.tagged).toBe("projects");
    expect(r.tags).toEqual(["Experiential", "Test"]);
    expect(r.media).toEqual(["alt,height,kind,poster,src,width", "alt,height,kind,poster,src,width"]);
    expect(r.refused).toEqual([false, false, false, false, false, false, false]);

    // the parent ignores a forged site:open with a non-http(s) URL, even after a click
    let opened = 0;
    context.on("page", () => opened++);
    await frame.locator("body").click({ position: { x: 5, y: 300 } });
    await frame.evaluate(() => {
      parent.postMessage({ v: 1, type: "site:open", url: "javascript:alert(1)" }, "*");
      parent.postMessage({ v: 1, type: "site:open", url: "data:text/html,<script>alert(1)</script>" }, "*");
    });
    await page.waitForTimeout(700);
    expect(opened).toBe(0);
    // and opens an https one (the click's activation reaches the parent)
    await page.waitForTimeout(500);
    const popup = context.waitForEvent("page");
    await frame.locator("body").click({ position: { x: 5, y: 300 } });
    expect(await frame.evaluate(() => (window as any).site.openExternal("https://example.com/from-api"))).toBe(true);
    await expect.poll(async () => (await popup).url()).toBe("https://example.com/from-api");
  });
});
