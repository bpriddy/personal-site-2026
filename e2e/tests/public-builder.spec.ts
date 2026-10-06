// The public builder end to end, offline: the main site runs with
// BUILDER_DEMO_MODEL=1 (run.sh), so a visitor's prompt goes through the real
// flow with a canned model instead of Claude. Building happens in a modal on
// the site itself, and the only preview is the live site: the page's own
// front-end iframe switches to the visitor's revision in place, without
// navigating. Then Ben approves a submission in the admin.
// Needs the rotation from the store (run.sh phase "prompted").
import { test, expect, ROTATION, MAIN, waitLive, decodeToken, tokenFromURL } from "./support";
import type { Page } from "@playwright/test";

test.skip(ROTATION.length > 0, "needs the rotation from the store (run.sh phase prompted)");

const dialogOf = (page: Page) => page.getByRole("dialog", { name: "Re-imagine this site" });

/** The servable ref the front-end iframe's token carries. */
async function servedRef(page: Page): Promise<string> {
  const src = await page.locator("iframe#frontend").getAttribute("src");
  return src ? decodeToken(tokenFromURL(src)).r : "";
}

/** Marks the document, so a later check proves the page never navigated. */
async function markPage(page: Page) {
  await page.evaluate(() => ((window as any).__e2eSamePage = true));
}
async function expectSamePage(page: Page) {
  expect(await page.evaluate(() => (window as any).__e2eSamePage === true), "the page navigated").toBe(true);
}

test("the site bar's button opens the builder modal; Esc closes it and focus returns", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("#transcript .site-header a.make-own-link")).toHaveAttribute("href", "/?build=1");
  const button = page.locator("#make-own");
  await expect(button).toBeVisible();
  await waitLive(page);
  await expect(button).toBeVisible(); // still there over the live front end
  // in the site bar across the top (with the concept line), at the right, and
  // the topmost element there; the front end starts below the bar
  const bar = page.locator("#site-bar");
  await expect(bar).toContainText("This site is re-imagined by its visitors");
  const box = (await button.boundingBox())!, bb = (await bar.boundingBox())!;
  const vp = page.viewportSize()!;
  expect(bb.y).toBe(0);
  expect(vp.width - (box.x + box.width)).toBeLessThan(48);
  expect(box.y).toBeGreaterThanOrEqual(bb.y);
  expect(box.y + box.height).toBeLessThanOrEqual(bb.y + bb.height);
  expect(await page.evaluate(([x, y]) => document.elementFromPoint(x, y)?.closest("#make-own") !== null,
    [box.x + box.width / 2, box.y + box.height / 2])).toBe(true);
  const fb = (await page.locator("iframe#frontend").boundingBox())!;
  expect(Math.abs(fb.y - (bb.y + bb.height))).toBeLessThan(2);

  // a click lands on it (not the front end) and opens the dialog, in place
  await markPage(page);
  await button.click();
  const dialog = dialogOf(page);
  await expect(dialog).toBeVisible();
  await expect(dialog).toHaveAttribute("aria-modal", "true");
  await expect(page).toHaveURL(`${MAIN}/`);
  await expect(page.locator("#bm-prompt")).toBeFocused();
  await expect(dialog.getByRole("button", { name: "Build a new site" })).toBeVisible();
  await expect(dialog.getByRole("heading", { name: "Your creations" })).toBeVisible();
  // the prompt comes first; your creations sit below it, folded until opened
  await expect(dialog.locator(".bm-mine")).not.toHaveAttribute("open", /.*/);
  const [promptY, mineY] = await Promise.all([page.locator("#bm-prompt"), dialog.locator(".bm-mine")].map(async (l) => (await l.boundingBox())!.y));
  expect(promptY).toBeLessThan(mineY);
  // no preview iframe in the modal: the site is the preview
  await expect(dialog.locator("iframe")).toHaveCount(0);

  // focus stays inside: tabbing past the last control wraps around
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press("Tab");
    expect(await page.evaluate(() => !!document.activeElement?.closest("#build-modal"))).toBe(true);
  }

  // Esc closes it; focus goes back to the Re-imagine button
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(button).toBeFocused();

  // the close button and a click outside close it too
  await button.click();
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toBeHidden();
  await button.click();
  await expect(dialog).toBeVisible();
  await page.mouse.click(10, 10); // the backdrop, outside the panel
  await expect(dialog).toBeHidden();
  await expectSamePage(page);

  // old links open it: /build and /build/<slug> redirect to /?build=1, which
  // opens the modal and cleans the URL
  for (const path of ["/build", "/build/whatever"]) {
    await page.goto(path);
    await expect(dialogOf(page)).toBeVisible();
    await expect(page).toHaveURL(`${MAIN}/`);
  }
});

test("a visitor builds in the modal and sees it live; picks versions; nobody else sees it; Ben approves", async ({ browser }) => {
  const a = await browser.newContext();
  const pa = await a.newPage();
  await pa.goto(`${MAIN}/`);
  await waitLive(pa);
  const before = await servedRef(pa);
  await markPage(pa);

  // build from the site bar
  await pa.locator("#make-own").click();
  const dialog = dialogOf(pa);
  const prompt = `e2e public ${Date.now().toString(36)}`;
  await pa.locator("#bm-prompt").fill(prompt);
  await dialog.getByRole("button", { name: "Build a new site" }).click();

  // the run streams in the modal, then the site switches to version 1 by itself
  const card = dialog.locator(".bm-fe", { hasText: prompt.replace(/^e/, "E") });
  await expect(card.locator(".bm-rev")).toHaveCount(1, { timeout: 20_000 });
  const r1Button = card.locator(".bm-rev").first();
  await expect(r1Button).toHaveAttribute("aria-pressed", "true", { timeout: 10_000 });
  const r1 = (await r1Button.getAttribute("data-rev"))!;
  expect(r1).toMatch(/^[a-z0-9]+$/);
  await expect.poll(() => servedRef(pa), { timeout: 15_000 }).toBe(`rev/${r1}`);
  expect(before).not.toBe(`rev/${r1}`);
  await waitLive(pa);
  await expect(pa.locator("html")).toHaveClass(/(^|\s)fe-draft(\s|$)/);
  await expect(pa.locator("#draft-banner")).toContainText("only you can see this");
  await expect(pa.locator("#draft-banner")).toContainText("version 1");
  await expect(pa.frameLocator("iframe#frontend").locator("body")).toHaveAttribute("data-demo", "1");
  await expect(dialog.locator(".bm-progress")).toContainText("Version 1 is on the site now");
  await expect(card.locator(".bm-chip").first()).toHaveText("Draft");
  await expect(card).toContainText(prompt);
  await expect(pa).toHaveURL(`${MAIN}/`);
  await expectSamePage(pa);
  const slug = (await card.getAttribute("data-slug"))!;
  expect(slug).toMatch(/^e2e-public-[a-z0-9-]+$/);

  // reprompt: the form now changes the version on the site
  await expect(pa.locator("label[for=bm-prompt]")).toContainText("What should change in version 1");
  await pa.locator("#bm-prompt").fill("make it warmer");
  await dialog.getByRole("button", { name: /^Change version \d+$/ }).click();
  await expect(card.locator(".bm-rev")).toHaveCount(2, { timeout: 20_000 });
  const r2Button = card.locator(".bm-rev").first(); // newest first
  await expect(r2Button).toHaveAttribute("aria-pressed", "true", { timeout: 10_000 });
  const r2 = (await r2Button.getAttribute("data-rev"))!;
  expect(r2).not.toBe(r1);
  await expect(r2Button).toContainText("from version 1");
  await expect(r2Button).toContainText("make it warmer");
  await expect.poll(() => servedRef(pa), { timeout: 15_000 }).toBe(`rev/${r2}`);
  await expect(pa.locator("#draft-banner")).toContainText("version 2");
  await waitLive(pa);

  // pick version 1 again: the site switches back
  // open "Your creations" (folded by default) to pick a version
  await dialog.locator(".bm-mine-sum").click();
  await expect(dialog.locator(".bm-mine")).toHaveAttribute("open", /.*/);
  // each creation's versions are folded too (v1.9)
  await expect(card.locator(".bm-versions")).not.toHaveAttribute("open", /.*/);
  await card.locator(".bm-versions-sum").click();
  await card.locator(`.bm-rev[data-rev="${r1}"]`).click();
  await expect(card.locator(`.bm-rev[data-rev="${r1}"]`)).toHaveAttribute("aria-pressed", "true");
  await expect(card.locator(`.bm-rev[data-rev="${r2}"]`)).toHaveAttribute("aria-pressed", "false");
  await expect.poll(() => servedRef(pa), { timeout: 15_000 }).toBe(`rev/${r1}`);
  await expect(pa.locator("#draft-banner")).toContainText("version 1");
  await waitLive(pa);
  await expectSamePage(pa);

  // a second browser sees none of it, even with forged fe_live cookies
  const b = await browser.newContext();
  await b.addCookies([{ name: "fe_live", value: `fe/${slug}:${r1}`, url: MAIN }]);
  const pb = await b.newPage();
  const api = await (await pb.request.get(`${MAIN}/api/frontend`)).json();
  expect(api.draft).toBeUndefined();
  expect(api.ref).not.toBe(`fe/${slug}`);
  const list = await (await pb.request.get(`${MAIN}/build/api/frontends`)).text();
  expect(list).not.toContain(prompt);
  expect(list).not.toContain(r1);
  for (const path of [`/build/api/fe/${slug}/live`, `/build/api/fe/${slug}/submit`]) {
    expect((await pb.request.post(`${MAIN}${path}`, { data: { rev: r1 } })).status(), path).toBe(404);
  }
  await pb.goto(`${MAIN}/build/${slug}`); // an old link: just opens their own (empty) builder
  await expect(dialogOf(pb)).toBeVisible();
  await dialogOf(pb).locator(".bm-mine-sum").click(); // "Your creations" is folded until opened
  await expect(dialogOf(pb).locator(".bm-empty")).toBeVisible();
  await expect(dialogOf(pb)).not.toContainText(prompt);
  await waitLive(pb);
  await expect(pb.locator("#draft-banner")).toHaveCount(0);
  expect(await servedRef(pb)).not.toBe(`rev/${r1}`);
  await b.close();

  // Exit: back to the normal site, in place
  await dialog.getByRole("button", { name: "Back to the normal site" }).click();
  await expect(pa.locator("#draft-banner")).toHaveCount(0, { timeout: 10_000 });
  await expect.poll(async () => {
    const r = await servedRef(pa);
    return r !== "" && r !== `rev/${r1}` && r !== `rev/${r2}`;
  }, { timeout: 15_000 }).toBe(true);
  await expect(pa.locator("html")).not.toHaveClass(/(^|\s)fe-draft(\s|$)/);
  await waitLive(pa);
  await expect(card.locator('.bm-rev[aria-pressed="true"]')).toHaveCount(0);
  await expectSamePage(pa);

  // submit (the newest version, nothing being on the site)
  await card.getByRole("button", { name: "Submit version 2 for review" }).click();
  await expect(card.locator(".bm-fe-head .bm-chip")).toHaveText("Waiting for review");
  await expect(card.getByRole("button", { name: "Version 2 is with Ben" })).toBeDisabled();

  // the banner's Exit works too, also in place
  await card.locator(`.bm-rev[data-rev="${r2}"]`).click();
  await expect.poll(() => servedRef(pa), { timeout: 15_000 }).toBe(`rev/${r2}`);
  await pa.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await pa.locator("#draft-banner").getByRole("button", { name: "Exit" }).click();
  await expect(pa.locator("#draft-banner")).toHaveCount(0, { timeout: 10_000 });
  await waitLive(pa);
  await expectSamePage(pa);

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
  await pa.locator("#make-own").click();
  await expect(card.locator(".bm-fe-head .bm-chip")).toHaveText("Approved");
  await a.close();
  const c = await browser.newContext();
  await c.addCookies([{ name: "fe_pick", value: `fe/${slug}`, url: MAIN }]);
  const pc = await c.newPage();
  await pc.goto(`${MAIN}/`);
  await waitLive(pc);
  expect(await servedRef(pc)).toBe(`rev/${r2}`);
  await expect(pc.locator("#draft-banner")).toHaveCount(0);
  await c.close();
});

test("shuffle switches to another front end in place, next to Re-imagine", async ({ page }) => {
  await page.goto("/");
  await waitLive(page);
  const shuffle = page.locator("#shuffle-fe");
  const cta = page.locator("#make-own");
  await expect(cta).toHaveAccessibleName("Re-imagine this site");
  await expect(shuffle).toBeVisible(); // the store rotation holds more than one front end
  await expect(shuffle).toHaveAccessibleName(/Shuffle/);
  // side by side in the site bar, shuffle to the left
  const sb = (await shuffle.boundingBox())!, cb = (await cta.boundingBox())!;
  expect(sb.x + sb.width).toBeLessThanOrEqual(cb.x + 1);
  expect(Math.abs((sb.y + sb.height / 2) - (cb.y + cb.height / 2))).toBeLessThan(4);

  const refOf = async () => decodeToken(tokenFromURL((await page.locator("iframe#frontend").getAttribute("src"))!)).r;
  const before = await refOf();
  const url = page.url();
  await shuffle.click();
  await expect.poll(refOf, { timeout: 15_000 }).not.toBe(before);
  await waitLive(page);
  expect(page.url()).toBe(url); // no navigation: the iframe was swapped in place
  // the pick sticks for the visit
  const after = await refOf();
  await page.reload();
  await waitLive(page);
  expect(await refOf()).toBe(after);
});
