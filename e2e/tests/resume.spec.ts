// /resume (v1.14): one fixed page outside the rotation. The career as a
// system (an SVG drawn from the page's JSON), the plain resume under it, and
// a PDF made from the same data.
import { test, expect } from "./support";

test("/resume draws the system, opens a node, and offers the PDF", async ({ page, request }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/resume");
  await expect(page.locator("h1.rz-name")).not.toBeEmpty();
  // no front end here: it's the same page for everyone
  await expect(page.locator("iframe")).toHaveCount(0);
  const nodes = page.locator(".rz-node");
  await expect.poll(() => nodes.count()).toBeGreaterThan(1);
  // a node opens its panel; Escape closes it
  const me = page.locator('.rz-node[data-kind="me"] circle');
  await page.waitForTimeout(2500); // the burst settles
  await me.scrollIntoViewIfNeeded();
  const box = (await me.boundingBox())!;
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  await expect(page.locator("#rz-panel")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator("#rz-panel")).toBeHidden();
  // the plain resume is there for readers and search
  await expect(page.locator(".rz-doc")).toContainText("Profile");
  const pdf = await request.get("/resume.pdf");
  expect(pdf.status()).toBe(200);
  expect(pdf.headers()["content-type"]).toBe("application/pdf");
  expect(errors).toEqual([]);
});
