// (f) What the front end can't do from inside the sandboxed iframe.
import { test, expect, MAIN, waitLive, frontendFrame } from "./support";

test("the iframe is sandboxed: no cookies, storage, main-origin fetch or top access", async ({ page }) => {
  await page.goto("/");
  await waitLive(page);
  const frame = await frontendFrame(page);

  const r = await frame.evaluate(async (main) => {
    const attempt = async (fn: () => unknown) => {
      try {
        return { ok: true, value: String(await fn()) };
      } catch (e) {
        return { ok: false, value: String(e) };
      }
    };
    return {
      origin: String(self.origin),
      cookie: await attempt(() => document.cookie),
      localStorage: await attempt(() => localStorage.getItem("x")),
      sessionStorage: await attempt(() => sessionStorage.getItem("x")),
      fetchMain: await attempt(async () => (await fetch(`${main}/api/site.json`)).status),
      fetchMainNoCors: await attempt(async () => (await fetch(`${main}/api/site.json`, { mode: "no-cors" })).type),
      topHref: await attempt(() => window.top!.location.href),
      topDocument: await attempt(() => window.top!.document.title),
    };
  }, MAIN);

  expect(r.origin, "opaque origin").toBe("null");
  expect(!r.cookie.ok || r.cookie.value === "", `document.cookie: ${r.cookie.value}`).toBe(true);
  expect(r.localStorage.ok, "localStorage should throw").toBe(false);
  expect(r.sessionStorage.ok, "sessionStorage should throw").toBe(false);
  expect(r.fetchMain.ok, `fetch main origin: ${r.fetchMain.value}`).toBe(false);
  expect(r.fetchMainNoCors.ok, `no-cors fetch main origin (CSP connect-src): ${r.fetchMainNoCors.value}`).toBe(false);
  expect(r.topHref.ok, "reading top.location.href should throw").toBe(false);
  expect(r.topDocument.ok, "reading top.document should throw").toBe(false);
});

test("the iframe can't navigate the top page", async ({ page }) => {
  await page.goto("/");
  await waitLive(page);
  const frame = await frontendFrame(page);
  const url = page.url();
  await frame.evaluate((main) => {
    try {
      window.top!.location.href = `${main}/pwned`;
    } catch {}
  }, MAIN);
  await page.waitForTimeout(1_000);
  expect(page.url()).toBe(url);
});
