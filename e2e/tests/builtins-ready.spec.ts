// (b) Each built-in front end reaches ready (html.fe-live) when it's the only
// ref in the rotation. run.sh starts the servers once per ref with
// FRONTEND_ROTATION=<ref>; the test for the other refs skips.
import { test, expect, requireRotation, waitLive, decodeToken, tokenFromURL, BUILTINS } from "./support";

for (const ref of BUILTINS) {
  test(`${ref} reaches ready`, async ({ page, parentErrors }) => {
    requireRotation(ref);
    await page.goto("/");
    await waitLive(page);

    // it's the forced ref that went live, not the fallback
    const src = (await page.locator("iframe").getAttribute("src"))!;
    expect(decodeToken(tokenFromURL(src)).r).toBe(ref);
    await expect(page.locator("html")).not.toHaveClass(/fe-loading/);

    // stays live for a moment (no late gpu-lost / error swap)
    await page.waitForTimeout(2_000);
    await expect(page.locator("html")).toHaveClass(/fe-live/);
    expect(await parentErrors()).toEqual([]);
  });
}
