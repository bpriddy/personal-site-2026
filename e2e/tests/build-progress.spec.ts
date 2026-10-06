// A build in progress in the public builder (v1.11): the sheet and the site
// bar turn to moving stripes, the prompt being built shows with a clear
// Cancel, the prompt box waits, all of it survives a reload, and Cancel
// stops the build with nothing changed. Offline demo model, 2s per turn
// (run.sh BUILDER_DEMO_DELAY); needs the rotation from the store.
import { test, expect, ROTATION } from "./support";

test.skip(ROTATION.length > 0, "needs the rotation from the store (run.sh phase prompted)");

test("a build in progress: stripes, the prompt, a disabled box, survives a reload, cancels", async ({ page }) => {
  await page.goto("/");
  await page.locator("#make-own").click();
  const dialog = page.getByRole("dialog", { name: "Re-imagine this site" });
  const prompt = "A slow brutalist page, all concrete";
  await dialog.locator("#bm-prompt").fill(prompt);
  await dialog.getByRole("button", { name: "Build" }).click();

  const html = page.locator("html");
  const card = dialog.locator(".bm-gen");
  await expect(html).toHaveClass(/bm-generating/);
  await expect(card).toBeVisible();
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
  await expect(page.locator("#build-pill")).toBeVisible();
  await page.locator("#build-pill").click();
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
