// A build in progress in the public builder (v1.11): the sheet and the site
// bar turn to moving stripes, the prompt being built shows with a clear
// Cancel, the prompt box waits, all of it survives a reload, and Cancel
// stops the build with nothing changed. Offline demo model, 2s per turn
// (run.sh BUILDER_DEMO_DELAY); needs the rotation from the store.
import { test, expect, ROTATION } from "./support";

test.skip(ROTATION.length > 0, "needs the rotation from the store (run.sh phase prompted)");

test("a build in progress: stripes, the prompt, a disabled box, survives a reload, cancels", async ({ page }) => {
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.goto("/");
  await page.locator("#make-own").click();
  const dialog = page.getByRole("dialog", { name: "Re-imagine this site" });
  const prompt = "A slow brutalist page, all concrete";
  await dialog.locator("#bm-prompt").fill(prompt);
  await dialog.getByRole("button", { name: "Build a new site" }).click();

  const html = page.locator("html");
  const card = dialog.locator(".bm-gen");
  await expect(html).toHaveClass(/bm-generating/);
  await expect(card).toBeVisible();
  // Copy puts the full prompt on the clipboard
  await card.getByRole("button", { name: "Copy the prompt being built" }).click();
  await expect(card.locator(".bm-copy")).toHaveText("Copied");
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(prompt);
  await expect(page.locator("#make-own .make-own-status")).toHaveText("Building…");

  await expect(card.locator(".bm-gen-prompt")).toHaveText(`“${prompt}”`);
  await expect(dialog.locator("#bm-prompt")).toBeDisabled();
  await expect(card.getByRole("button", { name: "Cancel this build" })).toBeEnabled();
  const stripes = (sel: string) => page.locator(sel).evaluate((n) => getComputedStyle(n).backgroundImage);
  expect(await stripes(".bm-dialog")).toContain("repeating-linear-gradient");
  expect(await stripes("#site-bar")).toContain("repeating-linear-gradient");
  if (process.env.E2E_SHOT_DIR) {
    await page.screenshot({ path: `${process.env.E2E_SHOT_DIR}/building-light.png` });
    await page.emulateMedia({ colorScheme: "dark" });
    await page.screenshot({ path: `${process.env.E2E_SHOT_DIR}/building-dark.png` });
    await page.emulateMedia({ colorScheme: "light" });
  }

  // a reload mid-build: the page picks it back up from the server
  await page.reload();
  await expect(html).toHaveClass(/bm-generating/, { timeout: 5_000 });
  expect(await stripes("#site-bar")).toContain("repeating-linear-gradient");
  // the Re-imagine button, in its place, now says Building… and opens the builder
  const reimagine = page.locator("#make-own");
  await expect(reimagine).toBeVisible();
  await expect(reimagine.locator(".make-own-status")).toHaveText("Building…");
  await expect(page.locator("#build-pill")).toBeHidden();
  if (process.env.E2E_SHOT_DIR) {
    await page.locator("#site-bar").screenshot({ path: `${process.env.E2E_SHOT_DIR}/bar-light.png` });
    await page.emulateMedia({ colorScheme: "dark" });
    await page.waitForTimeout(700); // the button's colour transition
    await page.locator("#site-bar").screenshot({ path: `${process.env.E2E_SHOT_DIR}/bar-dark.png` });
    await page.emulateMedia({ colorScheme: "light" });
  }
  await reimagine.click();
  await expect(card).toBeVisible();
  await expect(card.locator(".bm-gen-prompt")).toHaveText(`“${prompt}”`);
  await expect(dialog.locator("#bm-prompt")).toBeDisabled();

  // cancel: the build stops, nothing new, the box opens again
  await card.getByRole("button", { name: "Cancel this build" }).click();
  await expect(html).not.toHaveClass(/bm-generating/, { timeout: 10_000 });
  await expect(card).toBeHidden();
  await expect(dialog.locator("#bm-prompt")).toBeEnabled();
  if (process.env.E2E_SHOT_DIR) await page.screenshot({ path: `${process.env.E2E_SHOT_DIR}/canceled-light.png` });
  expect(await stripes("#site-bar")).not.toContain("repeating-linear-gradient");
  const list = await page.evaluate(() => fetch("/build/api/frontends", { cache: "no-store" }).then((r) => r.json()));
  expect(list.frontends.every((f: any) => !f.running && f.revisions.length === 0)).toBe(true);
});

test("during a build the visitor can still switch between their versions", async ({ page }) => {
  await page.goto("/");
  await page.locator("#make-own").click();
  const dialog = page.getByRole("dialog", { name: "Re-imagine this site" });
  const prompt = `switching ${Date.now().toString(36)}`;
  await dialog.locator("#bm-prompt").fill(prompt);
  await dialog.getByRole("button", { name: "Build a new site" }).click();
  const card = dialog.locator(".bm-fe", { hasText: prompt.replace(/^s/, "S") });
  await expect(card.locator(".bm-rev")).toHaveCount(1, { timeout: 20_000 });
  await expect(dialog.locator(".bm-progress")).toContainText("Version 1 is on the site now", { timeout: 15_000 });
  await dialog.locator("#bm-prompt").fill("make it warmer");
  await dialog.getByRole("button", { name: /^Change version \d+$/ }).click();
  await expect(card.locator(".bm-rev")).toHaveCount(2, { timeout: 20_000 });
  await expect(dialog.locator(".bm-progress")).toContainText("Version 2 is on the site now", { timeout: 15_000 });

  // a third build, and while it runs: switch to version 1, then back to 2
  await dialog.locator("#bm-prompt").fill("make it cooler");
  await dialog.getByRole("button", { name: /^Change version \d+$/ }).click();
  await expect(page.locator("html")).toHaveClass(/bm-generating/);
  await dialog.locator(".bm-mine-sum").click();
  await card.locator(".bm-versions-sum").click();
  const [v2, v1] = [card.locator(".bm-rev").nth(0), card.locator(".bm-rev").nth(1)];
  await v1.click();
  await expect(v1).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("#draft-banner")).toContainText("version 1");
  await expect(page.locator("html")).toHaveClass(/bm-generating/); // still building
  await v2.click();
  await expect(v2).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("#draft-banner")).toContainText("version 2");
  // the build finishes as version 3, on the site
  await expect(card.locator(".bm-rev")).toHaveCount(3, { timeout: 20_000 });
  await expect(page.locator("html")).not.toHaveClass(/bm-generating/);
});

test("the prompt's target is always clear: the version on the site by default, the visitor's choice kept", async ({ page }) => {
  await page.goto("/");
  await page.locator("#make-own").click();
  const dialog = page.getByRole("dialog", { name: "Re-imagine this site" });
  // no creations yet: no switch, only a new site
  await expect(dialog.locator(".bm-target")).toBeHidden();
  const prompt = `target ${Date.now().toString(36)}`;
  await dialog.locator("#bm-prompt").fill(prompt);
  await dialog.getByRole("button", { name: "Build a new site" }).click();
  await expect(dialog.locator(".bm-progress")).toContainText("Version 1 is on the site now", { timeout: 20_000 });

  // after a reload, the version on the site is what the prompt changes
  await page.reload();
  await page.locator("#make-own").click();
  const change = dialog.getByRole("radio", { name: /Change/ });
  const fresh = dialog.getByRole("radio", { name: /New/ });
  await expect(change).toHaveAttribute("aria-checked", "true");
  await expect(change).toContainText("version 1");
  await expect(dialog.getByRole("button", { name: "Change version 1" })).toBeVisible();
  await expect(page.locator("label[for=bm-prompt]")).toContainText("What should change in version 1");
  if (process.env.E2E_SHOT_DIR) await dialog.screenshot({ path: `${process.env.E2E_SHOT_DIR}/target-change.png` });

  // choosing New sticks across a reload
  await fresh.click();
  await expect(fresh).toHaveAttribute("aria-checked", "true");
  await expect(dialog.getByRole("button", { name: "Build a new site" })).toBeVisible();
  await page.reload();
  await page.locator("#make-own").click();
  await expect(fresh).toHaveAttribute("aria-checked", "true");
  await expect(dialog.getByRole("button", { name: "Build a new site" })).toBeVisible();
  await change.click();
  await expect(dialog.getByRole("button", { name: "Change version 1" })).toBeVisible();

  // each version's prompt can be copied in full
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await dialog.locator(".bm-mine-sum").click();
  await dialog.locator(".bm-versions-sum").first().click();
  await dialog.getByRole("button", { name: "Copy the prompt for version 1" }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(prompt);
});
