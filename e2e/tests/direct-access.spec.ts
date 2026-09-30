// (g) The index URL only works as an iframe; (h) it expires after 60s.
// Plus the user-content HTTP contract from docs/frontend-protocol.md.
import { test, expect, MAIN, UC, waitLive, decodeToken, tokenFromURL } from "./support";

async function freshIndexURL(request: import("@playwright/test").APIRequestContext) {
  const res = await request.get(`${MAIN}/api/frontend`);
  expect(res.status()).toBe(200);
  expect(res.headers()["cache-control"]).toBe("no-store");
  const body = (await res.json()) as { ref: string; url: string };
  expect(body.url.startsWith(`${UC}/t/`)).toBe(true);
  expect(decodeToken(tokenFromURL(body.url)).r).toBe(body.ref);
  return body.url;
}

test("the iframe's index URL is refused outside an iframe", async ({ page, context, request }) => {
  await page.goto("/");
  await waitLive(page);
  const src = (await page.locator("iframe").getAttribute("src"))!;

  // API request: no Sec-Fetch-Dest at all
  const direct = await request.get(src);
  expect(direct.status()).toBe(403);

  // top-level navigation: Sec-Fetch-Dest: document
  const tab = await context.newPage();
  const res = await tab.goto(src);
  expect(res?.status()).toBe(403);
  await tab.close();
});

test("a fresh index URL works with Sec-Fetch-Dest: iframe and has the protocol headers", async ({ request }) => {
  const url = await freshIndexURL(request);
  const res = await request.get(url, { headers: { "Sec-Fetch-Dest": "iframe" } });
  expect(res.status()).toBe(200);
  const h = res.headers();
  const csp = h["content-security-policy"] ?? "";
  expect(csp).toMatch(/(^|;)\s*sandbox allow-scripts\s*(;|$)/);
  expect(csp).toContain(`frame-ancestors ${MAIN}`);
  expect(csp).toMatch(/connect-src 'self'/);
  expect(h["access-control-allow-origin"]).toBe("*");
  expect(h["cross-origin-resource-policy"]).toBe("cross-origin");
  expect(h["referrer-policy"]).toBe("no-referrer");
  expect(h["x-content-type-options"]).toBe("nosniff");
  expect(h["cache-control"]).toBe("no-store");
  // front ends load the host API
  expect(await res.text()).toContain("/site-host.js");
});

test("forged or tampered tokens get 403", async ({ request }) => {
  const url = await freshIndexURL(request);
  const tok = tokenFromURL(url);
  const [body, sig] = tok.split(".");
  const forgedBody = Buffer.from(JSON.stringify({ r: "builtin/particle-stream", i: Math.floor(Date.now() / 1000) })).toString("base64url");
  for (const bad of [`${forgedBody}.${sig}`, `${body}.${sig.slice(0, -2)}AA`, "garbage", `${body}`]) {
    const res = await request.get(`${UC}/t/${bad}/`, { headers: { "Sec-Fetch-Dest": "iframe" } });
    expect(res.status(), bad).toBe(403);
  }
});

test("user-content service: /site-host.js, /health, 404s", async ({ request }) => {
  const js = await request.get(`${UC}/site-host.js`);
  expect(js.status()).toBe(200);
  const text = await js.text();
  expect(text).not.toContain("__MAIN_ORIGIN__");
  expect(text).toContain(MAIN);
  expect(js.headers()["access-control-allow-origin"]).toBe("*");
  expect((await request.get(`${UC}/health`)).status()).toBe(200);
  expect((await request.get(`${UC}/`)).status()).toBe(404);
  expect((await request.get(`${UC}/nope`)).status()).toBe(404);

  const url = await freshIndexURL(request);
  const missing = await request.get(`${url}definitely-missing.js`);
  expect(missing.status()).toBe(404);
  const dotfile = await request.get(`${url}.env`);
  expect([403, 404]).toContain(dotfile.status());
});

test("the index URL expires after 60s @slow", async ({ request }) => {
  test.setTimeout(120_000);
  const url = await freshIndexURL(request);
  expect((await request.get(url, { headers: { "Sec-Fetch-Dest": "iframe" } })).status()).toBe(200);
  await new Promise((r) => setTimeout(r, 61_000));
  expect((await request.get(url, { headers: { "Sec-Fetch-Dest": "iframe" } })).status()).toBe(403);
});
