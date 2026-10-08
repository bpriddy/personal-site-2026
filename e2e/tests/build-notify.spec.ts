// "Email me when it's ready" and the countdown (v1.13). The dev server's mailer
// is an outbox (GET /dev/outbox); the demo model takes ~4s, so the build ends
// while the test watches. Needs the rotation from the store.
import { test, expect, ROTATION, MAIN } from "./support";

test.skip(ROTATION.length > 0, "needs the rotation from the store (run.sh phase prompted)");

test("a build counts down from 15:00 and emails when it's ready; the link hands it to another browser", async ({ page, browser, request }) => {
  const before = ((await (await request.get(`${MAIN}/dev/outbox`)).json()) ?? []).length;
  await page.goto("/");
  await page.locator("#make-own").click();
  const dialog = page.getByRole("dialog", { name: "Re-imagine this site" });
  await dialog.locator("#bm-prompt").fill(`notify ${Date.now().toString(36)}`);
  await dialog.getByRole("button", { name: "Build a new site" }).click();

  const card = dialog.locator(".bm-gen");
  await expect(card).toBeVisible();
  await expect(card.getByRole("timer")).toHaveText(/^1[45]:\d\d$/);
  await expect(card.locator(".bm-gen-clock-note")).toContainText("10–15 minutes");

  const offer = card.locator(".bm-gen-notify");
  await expect(offer).toContainText("We can email you when it's ready");
  const email = offer.getByRole("textbox", { name: "Your email" });
  await email.fill("not an address");
  await offer.getByRole("button", { name: "Email me" }).click();
  await expect(offer.getByRole("alert")).toHaveText("That doesn't look like an email address.");
  await email.fill("visitor@example.com");
  await offer.getByRole("button", { name: "Email me" }).click();
  await expect(offer).toContainText("We'll email v•••@example.com when it's ready.");
  if (process.env.E2E_SHOT_DIR) await page.screenshot({ path: `${process.env.E2E_SHOT_DIR}/notify-set.png` });

  // the build ends: one email, with a link
  await expect(page.locator("html")).not.toHaveClass(/bm-generating/, { timeout: 20_000 });
  let sent: any[] = [];
  await expect.poll(async () => {
    sent = (await (await request.get(`${MAIN}/dev/outbox`)).json()) ?? [];
    return sent.length;
  }).toBe(before + 1);
  const msg = sent[sent.length - 1];
  expect(msg.To).toBe("visitor@example.com");
  expect(msg.Subject).toBe("Your version of benpriddy.com is ready");
  const link = /https?:\/\/\S+\/build\/open\/\S+/.exec(msg.Text)![0];

  // on another device: the build moves there, with the new version live
  const phone = await browser.newContext();
  const p2 = await phone.newPage();
  await p2.goto(link);
  await expect(p2).toHaveURL(new RegExp(`^${MAIN}/$`));
  await expect(p2.getByRole("dialog", { name: "Re-imagine this site" })).toBeVisible();
  const list = await p2.evaluate(() => fetch("/build/api/frontends", { cache: "no-store" }).then((r) => r.json()));
  expect(list.frontends.length).toBe(1);
  expect(list.live).not.toBeNull();
  await phone.close();
  // and the browser that made it no longer has it
  const mine = await page.evaluate(() => fetch("/build/api/frontends", { cache: "no-store" }).then((r) => r.json()));
  expect(mine.frontends.map((f: any) => f.slug)).not.toContain(list.frontends[0].slug);
});

test("an expired or broken link says so", async ({ page }) => {
  await page.goto("/build/open/not-a-real-link");
  const dialog = page.getByRole("dialog", { name: "Re-imagine this site" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("That link has expired.");
});
