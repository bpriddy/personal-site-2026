// "Email me when it's ready" and the countdown (v1.13). The dev server's mailer
// is an outbox (GET /dev/outbox); the demo model takes ~4s, so the build ends
// while the test watches. Needs the rotation from the store.
import { test, expect, ROTATION, MAIN } from "./support";

test.skip(ROTATION.length > 0, "needs the rotation from the store (run.sh phase prompted)");

test("a build counts down from 15:00 and emails when it's ready; the link asks, then hands it to another browser", async ({ page, browser, request }) => {
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

  // on another device the link asks first; Not now leaves it where it is
  const phone = await browser.newContext();
  const p2 = await phone.newPage();
  const slugs = async (pg: any) => (await pg.evaluate(() => fetch("/build/api/frontends", { cache: "no-store" }).then((r) => r.json()))).frontends.map((f: any) => f.slug);
  const mineBefore = await slugs(page);
  await p2.goto(link);
  const d2 = p2.getByRole("dialog", { name: "Re-imagine this site" });
  const ask = d2.getByRole("region", { name: /Move “.+” to this browser\?/ });
  await expect(ask).toBeVisible();
  await expect(ask.getByRole("heading")).toBeFocused();
  if (process.env.E2E_SHOT_DIR) await p2.screenshot({ path: `${process.env.E2E_SHOT_DIR}/claim-ask.png` });
  await ask.getByRole("button", { name: "Not now" }).click();
  await expect(ask).toBeHidden();
  await expect(d2).toContainText("it stays where it is");
  expect(await slugs(p2)).toEqual([]);
  expect(await slugs(page)).toEqual(mineBefore);

  // the link again, and yes: it moves, and the site shows it
  await p2.goto(link);
  await d2.getByRole("button", { name: "Move it here" }).click();
  await expect(p2).toHaveURL(new RegExp(`^${MAIN}/$`));
  await expect(d2).toBeVisible();
  await expect(d2.getByRole("region", { name: /to this browser/ })).toBeHidden();
  const moved = await slugs(p2);
  expect(moved.length).toBe(1);
  const list = await p2.evaluate(() => fetch("/build/api/frontends", { cache: "no-store" }).then((r) => r.json()));
  expect(list.live).not.toBeNull();
  await phone.close();
  // and the browser that made it no longer has it
  expect(await slugs(page)).not.toContain(moved[0]);
});

test("an expired or broken link says so", async ({ page }) => {
  await page.goto("/build/open/not-a-real-link");
  const dialog = page.getByRole("dialog", { name: "Re-imagine this site" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("That link has expired.");
});
