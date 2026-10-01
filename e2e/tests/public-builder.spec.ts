// The public builder end to end, offline: the main site runs with
// BUILDER_DEMO_MODEL=1 (run.sh), so a visitor's prompt goes through the real
// flow (create, streamed chat, revision, preview, View live, submit) with a
// canned model instead of Claude. Then Ben approves it in the admin.
// Needs the rotation from the store (run.sh phase "prompted").
import { test, expect, ROTATION, MAIN, waitLive, decodeToken, tokenFromURL } from "./support";

test.skip(ROTATION.length > 0, "needs the rotation from the store (run.sh phase prompted)");

test("the front page links to the builder, in the transcript and over the front end", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("#transcript .site-header a.make-own-link")).toHaveAttribute("href", "/build");
  const button = page.locator("#make-own");
  await expect(button).toBeVisible();
  await expect(button).toHaveAttribute("href", "/build");
  await waitLive(page);
  await expect(button).toBeVisible(); // still there over the live front end
  // pinned to the bottom-right corner, and the topmost element there
  const box = (await button.boundingBox())!;
  const vp = page.viewportSize()!;
  expect(vp.width - (box.x + box.width)).toBeLessThan(40);
  expect(vp.height - (box.y + box.height)).toBeLessThan(40);
  expect(await page.evaluate(([x, y]) => document.elementFromPoint(x, y)?.closest("#make-own") !== null,
    [box.x + box.width / 2, box.y + box.height / 2])).toBe(true);
  // it's above the iframe: a click lands on it, not the front end
  await button.click();
  await expect(page).toHaveURL(/\/build$/);
  await expect(page.locator("h1")).toHaveText("Make your own version of this site");
});

test("a visitor builds, views live and submits; nobody else sees it; Ben approves", async ({ browser }) => {
  const a = await browser.newContext();
  const pa = await a.newPage();
  await pa.goto(`${MAIN}/build`);
  const prompt = `e2e public ${Date.now().toString(36)}`;
  await pa.locator("#prompt").fill(prompt);
  await pa.getByRole("button", { name: "Build it" }).click();

  // the prompt is sent on arrival; the demo model's revision loads in the preview
  await expect(pa).toHaveURL(/\/build\/e2e-public-[a-z0-9-]+\?rev=[a-z0-9]+$/, { timeout: 20_000 });
  const slug = new URL(pa.url()).pathname.split("/")[2];
  const preview = pa.locator("#preview");
  await expect(preview).toHaveAttribute("data-state", "ready", { timeout: 15_000 });
  const ref = (await preview.getAttribute("data-ref"))!;
  expect(ref).toMatch(/^rev\/[a-z0-9]+$/);
  await expect(pa.locator("#chat")).toContainText(prompt);
  await expect(pa.locator(".bl-chip")).toHaveText("Draft");

  // View live: the whole site with the draft, plus the parent's banner
  await pa.getByRole("button", { name: "View it live on the site" }).click();
  await expect(pa).toHaveURL(`${MAIN}/`);
  await waitLive(pa);
  await expect(pa.locator("#draft-banner")).toContainText("only you can see this");
  await expect(pa.locator("#make-own")).toBeVisible();
  const src = (await pa.locator("iframe#frontend").getAttribute("src"))!;
  expect(decodeToken(tokenFromURL(src)).r).toBe(ref);
  const frame = pa.frameLocator("iframe#frontend");
  await expect(frame.locator("body")).toHaveAttribute("data-demo", "1");

  // a second browser sees none of it, even with a forged fe_live cookie
  const b = await browser.newContext();
  await b.addCookies([{ name: "fe_live", value: `fe/${slug}`, url: MAIN }]);
  const pb = await b.newPage();
  const api = await (await pb.request.get(`${MAIN}/api/frontend`)).json();
  expect(api.draft).toBeUndefined();
  expect(api.ref).not.toBe(`fe/${slug}`);
  await pb.goto(`${MAIN}/`);
  await waitLive(pb);
  await expect(pb.locator("#draft-banner")).toHaveCount(0);
  for (const path of [`/build/${slug}`, `/build/preview?ref=${ref}`]) {
    expect((await pb.request.get(`${MAIN}${path}`)).status(), path).toBe(404);
  }
  await pb.goto(`${MAIN}/build`);
  await expect(pb.locator("body")).not.toContainText(prompt);
  await b.close();

  // Exit returns the visitor to the rotation
  await pa.locator("#draft-banner").getByRole("button", { name: "Exit" }).click();
  await expect(pa.locator("#draft-banner")).toHaveCount(0, { timeout: 10_000 });
  await waitLive(pa);

  // submit
  await pa.goto(`${MAIN}/build/${slug}`);
  await pa.getByRole("button", { name: /Submit version 1 for review/ }).click();
  await expect(pa.locator(".bl-head .bl-chip")).toHaveText("Waiting for review");

  // Ben's admin: the submission, a badge, approve
  const admin = await browser.newContext({ httpCredentials: { username: "admin", password: "dev" } });
  const ap = await admin.newPage();
  await ap.goto(`${MAIN}/admin/builder/`);
  await expect(ap.locator("#builder-dot")).toBeVisible();
  const row = ap.locator("#submissions tr", { hasText: `fe/${slug}` });
  await expect(row).toContainText("pending");
  await row.getByRole("button", { name: "Approve" }).click();
  await expect(ap.locator("#submissions tr", { hasText: `fe/${slug}` })).toContainText("approved");
  await admin.close();

  // the visitor sees it was approved; a fresh visitor whose pick it is gets it
  await pa.reload();
  await expect(pa.locator(".bl-head .bl-chip")).toHaveText("Approved");
  await a.close();
  const c = await browser.newContext();
  await c.addCookies([{ name: "fe_pick", value: `fe/${slug}`, url: MAIN }]);
  const pc = await c.newPage();
  await pc.goto(`${MAIN}/`);
  await waitLive(pc);
  const csrc = (await pc.locator("iframe#frontend").getAttribute("src"))!;
  expect(decodeToken(tokenFromURL(csrc)).r).toBe(ref);
  await expect(pc.locator("#draft-banner")).toHaveCount(0);
  await c.close();
});
