// (d) A broken front end (fixtures/e2e-broken, copied by run.sh into
// FRONTENDS_DIR/builtin/e2e-broken) fails, and the parent falls back to the
// default builtin/site.
import { test, expect, requireRotation, waitLive, decodeToken, tokenFromURL, DEFAULT_REF, SETTLE_TIMEOUT } from "./support";

test("a broken front end falls back to builtin/site", async ({ page, parentErrors }) => {
  requireRotation("builtin/e2e-broken");

  const picks: string[] = [];
  page.on("response", async (res) => {
    if (new URL(res.url()).pathname === "/api/frontend") picks.push(`${res.url()} -> ${(await res.json()).ref}`);
  });

  await page.goto("/");
  await waitLive(page, SETTLE_TIMEOUT);

  const src = (await page.locator("iframe").getAttribute("src"))!;
  expect(decodeToken(tokenFromURL(src)).r).toBe(DEFAULT_REF);
  await expect(page.locator("iframe")).toHaveCount(1);

  // the first pick was the broken one, then an explicit fallback request
  expect(picks[0]).toMatch(/\/api\/frontend -> builtin\/e2e-broken$/);
  expect(picks.some((p) => /\?fallback=1 -> builtin\/site$/.test(p)), picks.join("\n")).toBe(true);
  expect(await parentErrors()).toEqual([]);
});

test("?fallback=1 always returns the default ref and keeps the pick cookie", async ({ request }) => {
  requireRotation("builtin/e2e-broken");
  const first = await (await request.get("/api/frontend")).json();
  expect(first.ref).toBe("builtin/e2e-broken");
  const fb = await (await request.get("/api/frontend?fallback=1")).json();
  expect(fb.ref).toBe(DEFAULT_REF);
  expect(decodeToken(tokenFromURL(fb.url)).r).toBe(DEFAULT_REF);
  const again = await (await request.get("/api/frontend")).json();
  expect(again.ref).toBe("builtin/e2e-broken");
});
