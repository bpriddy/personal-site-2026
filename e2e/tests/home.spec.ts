// (a) The public shell: transcript in the DOM plus the sandboxed front-end iframe.
import { test, expect, UC, savePage } from "./support";

test("home has the transcript and a sandboxed front-end iframe", async ({ page }) => {
  await page.goto("/");

  // transcript <main> with the home title (visually hidden under fe-loading/fe-live,
  // but always in the DOM)
  const main = page.locator("#transcript main");
  await expect(main).toHaveCount(1);
  await expect(main.locator("h1")).toHaveText("Ben Priddy");

  const iframe = page.locator("iframe");
  await expect(iframe).toHaveCount(1);
  await expect(iframe).toHaveAttribute("sandbox", "allow-scripts");
  await expect(iframe).toHaveAttribute("aria-hidden", "true");
  await expect(iframe).toHaveAttribute("allow", "fullscreen");
  await expect(iframe).toHaveAttribute("title", "Site");
  const src = await iframe.getAttribute("src");
  expect(src).toMatch(new RegExp(`^${UC.replace(/[.]/g, "\\.")}/t/`));
});

test("admin accepts a same-origin form POST from the test client", async ({ page, request }) => {
  // The admin sits behind net/http CrossOriginProtection. Playwright's request
  // context sends no Sec-Fetch-Site and no Origin, which it allows.
  await savePage(request, { slug: "e2e-admin-check", title: "E2E Admin Check", body: "created by e2e" });
  await page.goto("/e2e-admin-check");
  await expect(page.locator("#transcript main h1")).toHaveText("E2E Admin Check");
});
