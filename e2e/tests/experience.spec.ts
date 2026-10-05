// (v1.8) Experience: imported roles reach the transcript (server-rendered, for
// search and screening tools) and front ends (site.experience()); unpublished
// roles stay out of both. Plus the search basics: robots.txt and sitemap.xml.
import { test, expect, MAIN, DEFAULT_REF, adminAuth, waitLive, frontendFrame, requireRotation } from "./support";

const roles = {
  projects: [],
  experience: [
    { slug: "e2e-now", role: "E2E Current Role", company: "E2E Co", start: "2026-03", end: "", current: true, note: "", order: 1, published: true },
    { slug: "e2e-past", role: "E2E Past Role", company: "Past Co", start: "2020-02", end: "2022-04", current: false, note: "One line.", order: 2, published: true },
    { slug: "e2e-draft", role: "E2E Draft Role", company: "Hidden Co", start: "2019", end: "2020", current: false, note: "", order: 3, published: false },
  ],
};

test("experience: transcript, front end, and drafts kept out", async ({ page, request }) => {
  requireRotation(DEFAULT_REF);
  const res = await request.post(`${MAIN}/admin/import/projects`, { headers: adminAuth, data: roles });
  expect(res.status(), await res.text()).toBe(200);

  // the transcript (what crawlers read)
  const html = await (await request.get(`${MAIN}/`)).text();
  expect(html).toContain("( Experience )");
  expect(html).toContain('<time datetime="2026-03">Mar 2026</time> to present');
  expect(html).toContain("Past Co");
  expect(html).not.toContain("E2E Draft Role");

  // the front end's host API
  await page.goto("/");
  await waitLive(page);
  const frame = await frontendFrame(page);
  const xp = await frame.evaluate(() => (window as any).site.experience().map((e: any) => [e.slug, e.role, e.end, e.current]));
  expect(xp).toEqual([["e2e-now", "E2E Current Role", "", true], ["e2e-past", "E2E Past Role", "2022-04", false]]);
  // the default front end renders it on the home page
  await expect(frame.locator(".xp-row")).toHaveCount(2);
  await expect(frame.locator(".xp-row").first()).toContainText("Mar 2026 to present");

  // tidy up: unpublish, so other specs see the usual content
  await request.post(`${MAIN}/admin/import/projects`, {
    headers: adminAuth, data: { projects: [], experience: roles.experience.map((r) => ({ ...r, published: false })) },
  });
});

test("robots.txt and sitemap.xml", async ({ request }) => {
  const robots = await request.get(`${MAIN}/robots.txt`);
  expect(robots.headers()["content-type"]).toContain("text/plain");
  expect(await robots.text()).toContain("Sitemap:");
  const sm = await request.get(`${MAIN}/sitemap.xml`);
  expect(sm.status()).toBe(200);
  expect(await sm.text()).toContain("<urlset");
});
