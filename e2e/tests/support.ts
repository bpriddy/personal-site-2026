// Shared helpers for the specs that run against the system under test
// (main site + user-content service, started by e2e/run.sh).
import { test as base, expect, request as pwRequest, type Frame, type Page, type APIRequestContext } from "@playwright/test";

export const MAIN = process.env.E2E_MAIN_ORIGIN ?? "http://localhost:8090";
export const UC = process.env.E2E_USERCONTENT_ORIGIN ?? "http://127.0.0.1:8091";
export const DEFAULT_REF = "builtin/site";
export const BUILTINS = ["builtin/site", "builtin/particle-stream", "builtin/stream"] as const;

// Worst case to settle: the 10s ready timeout for the picked front end, then
// again for the fallback, plus page load.
export const READY_TIMEOUT = 20_000;
export const SETTLE_TIMEOUT = 30_000;

/** The FRONTEND_ROTATION the servers were started with (run.sh exports it). */
export const ROTATION = (process.env.FRONTEND_ROTATION ?? "")
  .split(",")
  .map((s) => s.trim())
  .filter(Boolean);

/** Skip unless the servers were started with exactly this single-ref rotation. */
export function requireRotation(ref: string) {
  test.skip(
    !(ROTATION.length === 1 && ROTATION[0] === ref),
    `needs the servers started with FRONTEND_ROTATION=${ref} (run.sh does this per phase)`,
  );
}

/** Decodes a front-end token (base64url(JSON{"r","i"}) "." sig) without verifying it. */
export function decodeToken(token: string): { r: string; i: number } {
  const body = token.split(".")[0];
  return JSON.parse(Buffer.from(body, "base64url").toString("utf8"));
}

/** Pulls the token out of a USERCONTENT_ORIGIN/t/<token>/... URL. */
export function tokenFromURL(url: string): string {
  const m = new URL(url).pathname.match(/^\/t\/([^/]+)\//);
  if (!m) throw new Error(`not a /t/<token>/ URL: ${url}`);
  return m[1];
}

export const adminAuth = { Authorization: "Basic " + Buffer.from("admin:dev").toString("base64") };

/** Creates or updates a published page through the admin form. */
export async function savePage(req: APIRequestContext, p: { slug: string; title: string; body: string }) {
  const res = await req.post(`${MAIN}/admin/pages/edit`, {
    headers: adminAuth,
    form: { slug: p.slug, title: p.title, body: p.body, published: "on" },
    maxRedirects: 0,
  });
  expect(res.status(), `admin save of /${p.slug}: ${await res.text()}`).toBe(303);
}

export const htmlClass = (page: Page) => page.locator("html");
export const liveClass = /(^|\s)fe-live(\s|$)/;

/** Waits until the parent switches to fe-live. */
export async function waitLive(page: Page, timeout = READY_TIMEOUT) {
  await expect(htmlClass(page)).toHaveClass(liveClass, { timeout });
}

/** The current front-end iframe's Frame (follows the fallback swap). */
export async function frontendFrame(page: Page): Promise<Frame> {
  const handle = await page.locator("iframe").elementHandle();
  const frame = await handle?.contentFrame();
  if (!frame) throw new Error("no front-end iframe");
  await frame.waitForFunction(() => typeof (window as any).site === "object");
  return frame;
}

type Fixtures = { parentErrors: () => Promise<string[]> };
type WorkerFixtures = { sutUp: void };

export const test = base.extend<Fixtures, WorkerFixtures>({
  // Fail with a clear message when the servers aren't running.
  sutUp: [
    async ({}, use) => {
      const ctx = await pwRequest.newContext();
      for (const origin of [MAIN, UC]) {
        const ok = await ctx
          .get(`${origin}/health`, { timeout: 3_000 })
          .then((r) => r.ok())
          .catch(() => false);
        if (!ok) throw new Error(`system under test not reachable at ${origin}/health; start it with e2e/run.sh`);
      }
      await ctx.dispose();
      await use();
    },
    { scope: "worker", auto: true },
  ],

  // Uncaught errors and unhandled rejections in the parent (main-origin) page only;
  // errors inside the front end are the parent's business, not the test's.
  parentErrors: [
    async ({ page }, use) => {
      await page.addInitScript((main) => {
        if (location.origin !== main) return;
        const errs: string[] = ((window as any).__e2eErrors = []);
        addEventListener("error", (e) => errs.push(`error: ${e.message}`));
        addEventListener("unhandledrejection", (e) => errs.push(`unhandledrejection: ${String(e.reason)}`));
      }, MAIN);
      await use(() => page.evaluate(() => (window as any).__e2eErrors ?? []));
    },
    { auto: true },
  ],
});

export { expect };
