// Prompted front ends end to end, without the model: a fixture revision is
// imported through the builder, published (made active + added to the
// rotation), served to a visitor as rev/<id>, and previewed in the admin.
// Needs the rotation from the store (run.sh phase "prompted": no
// FRONTEND_ROTATION), since the visitor's fe_pick must be in the rotation.
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { test, expect, ROTATION, MAIN, adminAuth, waitLive, frontendFrame, decodeToken, tokenFromURL } from "./support";

const FIXTURE = fileURLToPath(new URL("../fixtures/e2e-prompted/", import.meta.url));

test.skip(ROTATION.length > 0, "needs the rotation from the store (run.sh phase prompted)");

test("an imported revision is published, served to visitors and previewed", async ({ page, request, browser, parentErrors }) => {
  const slug = `e2e-${Date.now().toString(36)}`;
  const id = `fe/${slug}`;

  // create the front end
  let res = await request.post(`${MAIN}/admin/builder/new`, {
    headers: adminAuth, form: { slug, title: "E2E prompted" }, maxRedirects: 0,
  });
  expect(res.status(), await res.text()).toBe(303);

  // import the fixture as its first revision
  const files: Record<string, string> = {};
  for (const name of readdirSync(FIXTURE)) files[name] = readFileSync(FIXTURE + name, "utf8");
  res = await request.post(`${MAIN}/admin/builder/fe/${slug}/import`, {
    headers: adminAuth, data: { files, summary: "e2e fixture" },
  });
  expect(res.status(), await res.text()).toBe(200);
  const rev = (await res.json()) as { id: string; number: number; ref: string };
  expect(rev.number).toBe(1);
  expect(rev.ref).toBe(`rev/${rev.id}`);

  // adding an unpublished front end to the rotation publishes its latest
  // version (the rotation skips front ends without an active version)
  res = await request.post(`${MAIN}/admin/builder/rotation`, {
    headers: adminAuth, form: { id, in_rotation: "1" }, maxRedirects: 0,
  });
  expect(res.status()).toBe(303);

  // publish: make it active, add it to the rotation
  res = await request.post(`${MAIN}/admin/builder/fe/${slug}/activate`, {
    headers: adminAuth, form: { rev: rev.id }, maxRedirects: 0,
  });
  expect(res.status()).toBe(303);
  res = await request.post(`${MAIN}/admin/builder/rotation`, {
    headers: adminAuth, form: { id, in_rotation: "1" }, maxRedirects: 0,
  });
  expect(res.status()).toBe(303);

  // a visitor whose pick is this front end gets its active revision
  await page.context().addCookies([{ name: "fe_pick", value: id, url: MAIN }]);
  const api = await page.request.get(`${MAIN}/api/frontend`);
  const fe = await api.json();
  expect(fe.ref).toBe(id);
  expect(fe.serve).toBe(rev.ref);

  await page.goto("/");
  await waitLive(page);
  const src = (await page.locator("iframe#frontend").getAttribute("src"))!;
  expect(decodeToken(tokenFromURL(src)).r).toBe(rev.ref);
  const frame = await frontendFrame(page);
  await expect(frame.locator("body")).toHaveAttribute("data-e2e", "rendered");
  await expect(frame.locator("main h1")).toHaveText("Ben Priddy");

  // navigation from inside the front end goes through the parent
  await frame.locator('nav a[data-slug="experiments"]').click();
  await expect(page).toHaveURL(/\/experiments$/);
  await expect(frame.locator("main h1")).toHaveText("Experiments");
  await expect(page.locator("html")).toHaveClass(/fe-live/);
  expect(await parentErrors()).toEqual([]);

  // the admin preview runs the same revision with the host protocol
  const admin = await browser.newContext({ httpCredentials: { username: "admin", password: "dev" } });
  const ap = await admin.newPage();
  await ap.goto(`${MAIN}/admin/builder/fe/${slug}`);
  await expect(ap.locator("#preview")).toHaveAttribute("data-ref", rev.ref);
  await expect(ap.locator("#preview")).toHaveAttribute("data-state", "ready", { timeout: 15_000 });
  await expect(ap.locator("#preview-log")).toContainText("site:ready");
  await admin.close();
});
