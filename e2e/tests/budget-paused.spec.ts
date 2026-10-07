// Building paused for the budget (v1.12). The gate is forced closed for one
// browser by the dev-only dev_budget cookie (run.sh sets
// BUILD_BUDGET_DEV_COOKIE=1), so the other specs keep building. Checks: the
// site's conceit stays (the concept line, the Re-imagine button, the modal),
// the prompt box gives way to the paused state, existing versions still
// switch and submit, the idea keeper, a build that races the gate, the
// server refusing builds, and the kept idea coming back when it reopens.
import type { BrowserContext, Page } from "@playwright/test";
import { test, expect, ROTATION, MAIN } from "./support";

test.skip(ROTATION.length > 0, "needs the rotation from the store (run.sh phase prompted)");

const SHOTS = process.env.E2E_SHOT_DIR;

async function closeStudio(context: BrowserContext, limit: "day" | "month" | "later") {
  await context.addCookies([{ name: "dev_budget", value: limit, url: MAIN }]);
}
async function openStudio(context: BrowserContext) {
  await context.clearCookies({ name: "dev_budget" });
}
// a screenshot once the sheet has finished arriving
async function shot(page: Page, opts: { path: string }) {
  await page.waitForTimeout(800);
  await page.screenshot(opts);
}
function studio(page: Page) {
  return page.getByRole("dialog", { name: "Re-imagine this site" });
}

test("the studio closed for the day: the conceit stays, versions still switch, an idea is kept", async ({ page, context }) => {
  // two versions while it's open
  await page.goto("/");
  await page.locator("#make-own").click();
  const dialog = studio(page);
  const name = `paused ${Date.now().toString(36)}`;
  await dialog.locator("#bm-prompt").fill(name);
  await dialog.getByRole("button", { name: "Build a new site" }).click();
  await expect(dialog.locator(".bm-progress")).toContainText("Version 1 is on the site now", { timeout: 20_000 });
  await dialog.locator("#bm-prompt").fill("make it warmer");
  await dialog.getByRole("button", { name: /^Change version \d+$/ }).click();
  await expect(dialog.locator(".bm-progress")).toContainText("Version 2 is on the site now", { timeout: 20_000 });

  // the studio closes for today
  await closeStudio(context, "day");
  await page.reload();

  // the conceit stays: the concept line and the Re-imagine button, which hints
  // at the state without nagging (a hollow, still dot; words on hover and for
  // screen readers; the same label)
  const bar = page.locator("#site-bar");
  await expect(bar.locator(".site-bar-line")).toContainText("re-imagined by its visitors");
  const reimagine = page.locator("#make-own");
  await expect(reimagine).toBeVisible();
  await expect(reimagine).toBeEnabled();
  await expect(page.locator("html")).toHaveClass(/(^|\s)bm-paused(\s|$)/);
  await expect(reimagine).toHaveAttribute("aria-label", /closed for today/);
  await expect(reimagine).toHaveAttribute("title", /closed for today/);
  await expect(reimagine.locator(".make-own-roll")).toContainText("Re-imagine it");
  if (SHOTS) await bar.screenshot({ path: `${SHOTS}/paused-bar-light.png` });

  // the modal still opens; the paused state replaces the prompt box and is
  // read first (focus on its heading)
  await reimagine.click();
  await expect(dialog).toBeVisible();
  const paused = dialog.getByRole("region", { name: "The studio is closed for today." });
  await expect(paused).toBeVisible();
  await expect(paused.getByRole("heading", { name: "The studio is closed for today." })).toBeFocused();
  await expect(paused).toContainText("today's building time is used up. It opens again");
  await expect(dialog.locator(".bm-state")).toHaveText("Closed");
  await expect(dialog.locator("#bm-prompt")).toBeHidden();
  await expect(dialog.getByRole("button", { name: /Build|Change version/ })).toHaveCount(0);
  await expect(dialog).toHaveAttribute("aria-describedby", "bm-paused-text");
  for (const word of ["token", "API", "credit", "budget", "sorry", "$"]) {
    expect(await paused.innerText()).not.toContain(word);
  }
  if (SHOTS) {
    await page.waitForTimeout(800); // the sheet's entrance
    await page.screenshot({ path: `${SHOTS}/paused-day-light.png` });
    await page.emulateMedia({ colorScheme: "dark" });
    await page.screenshot({ path: `${SHOTS}/paused-day-dark.png` });
    await page.emulateMedia({ colorScheme: "light" });
  }

  // the keyboard: Tab moves on through the state's actions, Esc closes
  await page.keyboard.press("Tab");
  await expect(paused.getByRole("button", { name: "Your creations" })).toBeFocused();

  // existing versions: still switchable and submittable
  await paused.getByRole("button", { name: "Your creations" }).click();
  const card = dialog.locator(".bm-fe", { hasText: name.replace(/^p/, "P") });
  await expect(card).toBeVisible();
  await expect(card.getByRole("button", { name: "Change it" })).toHaveCount(0);
  await card.locator(".bm-versions-sum").click();
  const [v2, v1] = [card.locator(".bm-rev").nth(0), card.locator(".bm-rev").nth(1)];
  await v1.click();
  await expect(v1).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("#draft-banner")).toContainText("version 1");
  await v2.click();
  await expect(v2).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("#draft-banner")).toContainText("version 2");
  await card.getByRole("button", { name: "Submit version 2 for review" }).click();
  await expect(dialog.locator(".bm-notice")).toContainText("on its way to Ben");
  await expect(card.locator(".bm-chip-pending").first()).toBeVisible();

  // keep an idea for when it reopens (in this browser only)
  const idea = "A greenhouse at dusk, everything slowly fogging up";
  await paused.getByLabel("Keep an idea for when it reopens").fill(idea);
  await paused.getByRole("button", { name: "Keep this idea" }).click();
  await expect(paused.locator(".bm-idea-quote")).toHaveText(`“${idea}”`);
  await expect(paused).toContainText("When the studio reopens, it'll be waiting in the prompt box");
  await expect(paused.getByRole("button", { name: "Edit it" })).toBeFocused();
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem("bm-idea") || "null")?.text)).toBe(idea);
  if (SHOTS) await dialog.screenshot({ path: `${SHOTS}/paused-idea-kept.png` });
  await page.setViewportSize({ width: 390, height: 844 });
  if (SHOTS) {
    await page.reload();
    await page.locator("#make-own").click();
    await expect(paused).toBeVisible();
    await page.waitForTimeout(800);
    await page.screenshot({ path: `${SHOTS}/paused-day-phone.png` });
    await page.locator("#site-bar").screenshot({ path: `${SHOTS}/paused-bar-phone.png` });
  }

  // the server refuses builds while paused, the same way
  const refused = await page.evaluate(async () => {
    const r = await fetch("/build/api/new", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ prompt: "x" }) });
    return { status: r.status, body: await r.json() };
  });
  expect(refused.status).toBe(503);
  expect(refused.body.key).toBe("paused");
  expect(refused.body.paused.limit).toBe("day");
  const slug = await card.getAttribute("data-slug");
  const chat = await page.evaluate(async (slug) => {
    const r = await fetch(`/build/api/fe/${slug}/chat`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ prompt: "x" }) });
    return { status: r.status, paused: r.headers.get("X-Build-Paused") };
  }, slug);
  expect(chat).toEqual({ status: 503, paused: "day" });
  const state = await page.evaluate(() => fetch("/build/api/frontends", { cache: "no-store" }).then((r) => r.json()));
  expect(state.enabled).toBe(false);
  expect(state.paused.limit).toBe("day");
  expect(JSON.stringify(state)).not.toMatch(/usd|cost|\$/i);

  // another visitor's version: closes the studio and shuffles the site
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.reload();
  await page.locator("#make-own").click();
  await paused.getByRole("button", { name: "Show another version" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator("#draft-banner")).toBeHidden();

  // the studio reopens: the kept idea is back in the prompt box
  await openStudio(context);
  await page.reload();
  await expect(page.locator("html")).not.toHaveClass(/(^|\s)bm-paused(\s|$)/);
  await expect(page.locator("#make-own")).toHaveAttribute("aria-label", "Re-imagine this site");
  await page.locator("#make-own").click();
  await expect(dialog.locator("#bm-prompt")).toHaveValue(idea);
  await expect(dialog.locator(".bm-notice")).toContainText("Your saved idea is back in the box");
  await expect(paused).toBeHidden();
});

test("a build that races the gate comes back as the paused state, and keeps the prompt", async ({ page, context }) => {
  await page.goto("/");
  await page.locator("#make-own").click();
  const dialog = studio(page);
  const prompt = `raced ${Date.now().toString(36)}: a quiet library`;
  await dialog.locator("#bm-prompt").fill(prompt);
  // the budget runs out between opening the studio and pressing Build
  await closeStudio(context, "month");
  await dialog.getByRole("button", { name: "Build a new site" }).click();
  const paused = dialog.getByRole("region", { name: "The studio is closed for the rest of the month." });
  await expect(paused).toBeVisible();
  await expect(paused.getByRole("heading")).toBeFocused();
  await expect(dialog.locator(".bm-notice")).toContainText("closed just before your build could start");
  await expect(dialog.locator(".bm-status.bm-error")).toHaveCount(0);
  await expect(paused.locator(".bm-idea-quote")).toHaveText(`“${prompt}”`);
  await expect(page.locator("html")).not.toHaveClass(/bm-generating/);
  if (SHOTS) await shot(page, { path: `${SHOTS}/paused-month-raced.png` });
  // nothing was made
  const list = await page.evaluate(() => fetch("/build/api/frontends", { cache: "no-store" }).then((r) => r.json()));
  expect(list.frontends).toHaveLength(0);
  // forgetting the idea leaves the box empty
  await paused.getByRole("button", { name: "Forget it" }).click();
  await expect(paused.getByLabel("Keep an idea for when it reopens")).toBeFocused();
  expect(await page.evaluate(() => localStorage.getItem("bm-idea"))).toBeNull();
});

test("the API's own limit: closed for now, with no date", async ({ page, context }) => {
  await closeStudio(context, "later");
  await page.goto("/");
  await page.locator("#make-own").click();
  const paused = studio(page).getByRole("region", { name: "The studio is closed for now." });
  await expect(paused).toBeVisible();
  await expect(paused).toContainText("Check back a little later.");
  // nothing made yet: no creations to show, but other visitors' versions are there
  await expect(paused.getByRole("button", { name: "Your creations" })).toBeHidden();
  await expect(paused.getByRole("button", { name: "Show another version" })).toBeVisible();
  if (SHOTS) await shot(page, { path: `${SHOTS}/paused-later-empty.png` });
});

test("Ben sees spend against the limits on the Builder page", async ({ browser }) => {
  const ctx = await browser.newContext({ httpCredentials: { username: "admin", password: "dev" } });
  const page = await ctx.newPage();
  await page.goto(`${MAIN}/admin/builder/`);
  const spend = page.locator("#spend");
  await expect(spend.getByRole("heading", { name: /Spend/ })).toBeVisible();
  await expect(spend).toContainText("Today");
  await expect(spend).toContainText("This month");
  await expect(spend).toContainText("One front end ≈");
  await spend.getByText("Change the limits").click();
  await expect(spend.getByLabel("Daily limit (USD)")).toBeVisible();
  if (SHOTS) await spend.screenshot({ path: `${SHOTS}/admin-spend.png` });
  await ctx.close();
});
