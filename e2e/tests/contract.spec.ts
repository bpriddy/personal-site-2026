// (g) The content contract: front ends survive malformed, missing, extra and
// mistyped content, and the gaps they hit are reported to the observer
// (docs/observer.md, Layers 1-2; docs/frontend-protocol.md, "v1.1 additions").
import type { Page, Request } from "@playwright/test";
import {
  test,
  expect,
  MAIN,
  DEFAULT_REF,
  SETTLE_TIMEOUT,
  savePage,
  waitLive,
  frontendFrame,
  requireRotation,
  decodeToken,
  tokenFromURL,
} from "./support";

type Report = Record<string, string>;
const REPORT_KEYS = ["collection", "expect", "field", "frontend", "got", "item", "kind", "message", "route", "serve", "stack"];

/** Records every POST /api/observe the page sends (answering 204, as the observer does). */
async function captureReports(page: Page) {
  const reports: Report[] = [];
  const raw: Request[] = [];
  await page.route("**/api/observe", (route) => route.fulfill({ status: 204 }));
  page.on("request", (req) => {
    if (req.method() !== "POST" || new URL(req.url()).pathname !== "/api/observe") return;
    raw.push(req);
    try {
      reports.push(JSON.parse(req.postData() ?? ""));
    } catch {
      reports.push({ unparsable: req.postData() ?? "" });
    }
  });
  return { reports, raw };
}

/** Records the parent's /api/frontend picks, to prove there was no fallback. */
function capturePicks(page: Page) {
  const picks: string[] = [];
  page.on("request", (req) => {
    const u = new URL(req.url());
    if (u.pathname === "/api/frontend") picks.push(u.search);
  });
  return picks;
}

const LONG_BODY = Array.from({ length: 400 }, (_, i) => `Paragraph ${i}: ${"lorem ipsum dolor sit amet ".repeat(12)}`).join("\n\n");
const ODD_TITLE = `Ünïcödé ✓ 🚀 שלום مرحبا <script>alert(1)</script> "quotes" 'apos' & amp; ​ zero-width \\ backslash`;

test.describe("malformed content through the CMS", () => {
  test.beforeEach(async ({ request }) => {
    requireRotation(DEFAULT_REF);
    await savePage(request, { slug: "e2e-empty", title: "", body: "" });
    await savePage(request, { slug: "e2e-long", title: "Long", body: LONG_BODY });
    await savePage(request, { slug: "e2e-odd", title: ODD_TITLE, body: `${ODD_TITLE}\n\n\t  \n\n\u0001\u0007 control chars` });
  });

  test("/api/site.json is the contract: every declared field typed, published only", async ({ request }) => {
    const site = await (await request.get("/api/site.json")).json();
    expect(site.contractVersion).toBe(1);
    for (const [coll, fields] of [
      ["pages", ["slug", "title", "body"]],
      ["experiments", ["slug", "title", "summary"]],
    ] as const) {
      expect(Array.isArray(site[coll]), coll).toBe(true);
      for (const item of site[coll]) {
        for (const f of fields) expect(typeof item[f], `${coll}/${item.slug}.${f}`).toBe("string");
        expect(Array.isArray(item._generated), `${coll}/${item.slug}._generated`).toBe(true);
      }
    }
    const empty = site.pages.find((p: any) => p.slug === "e2e-empty");
    expect(empty).toEqual({ slug: "e2e-empty", title: "", body: "", _generated: [] });
    expect(site.pages.find((p: any) => p.slug === "e2e-odd").title).toBe(ODD_TITLE);
  });

  for (const slug of ["e2e-empty", "e2e-long", "e2e-odd"]) {
    test(`the default front end renders /${slug} without failing`, async ({ page, parentErrors }) => {
      const { reports } = await captureReports(page);
      const picks = capturePicks(page);
      await page.goto(`/${slug}`);
      await waitLive(page);
      const frame = await frontendFrame(page);
      await frame.waitForFunction((s) => (window as any).site.route === s, slug);

      // still the first pick: no fallback, no front-end error
      await page.waitForTimeout(500);
      expect(picks).toEqual([""]);
      await expect(page.locator("html")).toHaveClass(/fe-live/);
      expect(reports.filter((r) => r.kind === "frontend-error"), JSON.stringify(reports)).toEqual([]);
      expect(await parentErrors()).toEqual([]);

      // the transcript still has the page
      await expect(page.locator("#transcript main")).toHaveCount(1);
      if (slug === "e2e-odd") await expect(page.locator("#transcript main h1")).toHaveText(ODD_TITLE);
    });
  }

  test("the empty page is reported as content gaps, once each", async ({ page }) => {
    const { reports, raw } = await captureReports(page);
    await page.goto("/");
    await waitLive(page);
    const gap = (field: string) => reports.filter((r) => r.kind === "content-gap" && r.item === "e2e-empty" && r.field === field);
    await expect.poll(() => gap("title").length).toBe(1);
    await expect.poll(() => gap("body").length).toBe(1);
    const r = gap("title")[0];
    expect(Object.keys(r).sort()).toEqual(REPORT_KEYS);
    expect(r).toMatchObject({ kind: "content-gap", frontend: DEFAULT_REF, collection: "pages", expect: "text", got: "empty", route: "" });
    // a beacon to the same origin
    const req = raw.find((q) => q.postData()?.includes('"e2e-empty"'))!;
    expect(new URL(req.url()).origin).toBe(MAIN);
    expect(req.headers()["content-type"]).toMatch(/^(application\/json|text\/plain)/);
  });
});

test.describe("malformed payloads", () => {
  test("a malformed /api/site.json is normalized and the default front end still goes live", async ({ page, parentErrors }) => {
    requireRotation(DEFAULT_REF);
    const { reports } = await captureReports(page);
    const picks = capturePicks(page);
    await page.route("**/api/site.json", (route) =>
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({
          contractVersion: "one",
          pages: [
            { slug: "", title: 42, body: ["not", "a", "string"], subtitle: "extra field", _generated: "nope" },
            { slug: "no-fields" },
            null,
            "junk",
            [1, 2],
            { title: { nested: true }, body: null },
          ],
          experiments: { not: "an array" },
          futureCollection: [{ slug: "x" }],
        }),
      }),
    );
    await page.goto("/");
    await waitLive(page, SETTLE_TIMEOUT);
    expect(picks).toEqual([""]); // no fallback
    const frame = await frontendFrame(page);

    const content = await frame.evaluate(() => (window as any).site.content);
    expect(content.contractVersion).toBe(1);
    expect(content.experiments).toEqual([]);
    expect(content.futureCollection).toEqual([{ slug: "x" }]); // unknown keys pass through
    expect(content.pages).toEqual([
      { slug: "", title: "42", body: "", subtitle: "extra field", _generated: [], _collection: "pages" },
      { slug: "no-fields", title: "", body: "", _generated: [], _collection: "pages" },
      { slug: "", title: "", body: "", _generated: [], _collection: "pages" },
    ]);

    // the built-in front end read body through site.field: the array is a type break
    await expect
      .poll(() => reports.find((r) => r.kind === "type-break" && r.field === "body" && r.item === ""))
      .toMatchObject({ collection: "pages", expect: "text", got: "array", frontend: DEFAULT_REF });
    await expect.poll(() => reports.some((r) => r.kind === "content-gap" && r.item === "no-fields" && r.field === "title")).toBe(true);
    expect(reports.filter((r) => r.kind === "frontend-error")).toEqual([]);
    expect(await parentErrors()).toEqual([]);
  });
});

test.describe("host API", () => {
  test("site.get / pages / page / experiments / field never throw and fall back", async ({ page }) => {
    requireRotation(DEFAULT_REF);
    const { reports } = await captureReports(page);
    await page.goto("/");
    await waitLive(page);
    const frame = await frontendFrame(page);

    const r = await frame.evaluate(() => {
      const site = (window as any).site;
      const thrown: string[] = [];
      const t = (label: string, fn: () => unknown) => {
        try {
          return fn();
        } catch (e) {
          thrown.push(`${label}: ${e}`);
        }
      };
      const evil = {
        toString() {
          throw new Error("evil toString");
        },
      };
      const home = site.page("");
      const trap = new Proxy(
        {},
        {
          get() {
            throw new Error("trap");
          },
          getOwnPropertyDescriptor() {
            throw new Error("trap");
          },
        },
      );
      const out = {
        homeTitle: t("page", () => home.title),
        tagged: t("tags", () => [home._collection, Array.isArray(home._generated)]),
        pagesIsArray: t("pages", () => Array.isArray(site.pages())),
        experimentsIsArray: t("experiments", () => Array.isArray(site.experiments())),
        pageMissing: t("page missing", () => site.page("no-such-page")),
        pageEvil: t("page evil", () => site.page(evil)),
        getTitle: t("get", () => site.get("pages.0.title", "fb")),
        getMissing: t("get missing", () => site.get("pages.999.title", "fb")),
        getDeep: t("get deep", () => site.get("pages.0.title.x.y.z", "fb")),
        getWeird: t("get weird", () => [site.get(undefined, "u") === site.content, site.get(evil, "e"), site.get(42, "n"), site.get(["pages", 0, "slug"], "a")]),
        missingText: t("missing", () => site.field(home, "subtitle", { expect: "text", fallback: "No subtitle" })),
        missingDefault: t("missing default", () => site.field(home, "subtitle")),
        textAsList: t("list", () => site.field(home, "title", { expect: "list" })),
        textAsNumber: t("number", () => site.field(home, "title", { expect: "number", fallback: -1 })),
        textAsBool: t("bool", () => site.field(home, "title", { expect: "bool" })),
        again: t("dedupe", () => site.field(home, "subtitle", { expect: "text", fallback: "No subtitle" })),
        ok: t("ok", () => site.field(home, "title", { expect: "text", fallback: "fb" })),
        garbage: t("garbage", () => [
          site.field(),
          site.field(null, null),
          site.field(5, {}),
          site.field(home, evil, { fallback: "e" }),
          site.field(home, "title", "junk opts"),
          site.field(trap, "title", { fallback: "t" }),
          site.field({ title: "" }, "title", { fallback: "untagged" }),
        ]),
      };
      return { out, thrown };
    });

    expect(r.thrown).toEqual([]);
    const o = r.out as any;
    expect(o.homeTitle).toBe("Ben Priddy");
    expect(o.tagged).toEqual(["pages", true]);
    expect(o.pagesIsArray).toBe(true);
    expect(o.experimentsIsArray).toBe(true);
    expect(o.pageMissing).toEqual({});
    expect(o.pageEvil).toEqual({});
    expect(o.getTitle).toBe("Ben Priddy");
    expect(o.getMissing).toBe("fb");
    expect(o.getDeep).toBe("fb");
    expect(o.getWeird).toEqual([true, "e", "n", ""]);
    expect(o.missingText).toBe("No subtitle");
    expect(o.missingDefault).toBe("");
    expect(o.textAsList).toEqual([]);
    expect(o.textAsNumber).toBe(-1);
    expect(o.textAsBool).toBe(false);
    expect(o.again).toBe("No subtitle");
    expect(o.ok).toBe("Ben Priddy");
    expect(o.garbage).toEqual(["", "", "", "e", "Ben Priddy", "t", "untagged"]);

    // the parent forwarded the gap and the type breaks to /api/observe, once each
    const find = (kind: string, field: string, expectKind: string) =>
      reports.filter((x) => x.kind === kind && x.item === "" && x.field === field && x.expect === expectKind);
    await expect.poll(() => find("content-gap", "subtitle", "text").length).toBe(1);
    await expect.poll(() => find("type-break", "title", "list").length).toBe(1);
    // deduplicated per item and field: the later number/bool breaks on title aren't re-sent
    expect(find("type-break", "title", "number")).toEqual([]);
    expect(find("type-break", "title", "bool")).toEqual([]);
    const gap = find("content-gap", "subtitle", "text")[0];
    expect(Object.keys(gap).sort()).toEqual(REPORT_KEYS);
    expect(gap).toMatchObject({ frontend: DEFAULT_REF, serve: expect.any(String), collection: "pages", got: "missing", route: "" });
    expect(find("type-break", "title", "list")[0].got).toBe("string");
    // untagged objects and garbage produce no reports
    await page.waitForTimeout(300);
    expect(reports.filter((x) => x.kind !== "frontend-error" && x.collection !== "pages")).toEqual([]);
  });

  test("site:error after ready is reported to the observer without a fallback", async ({ page }) => {
    requireRotation(DEFAULT_REF);
    const { reports } = await captureReports(page);
    const picks = capturePicks(page);
    await page.goto("/");
    await waitLive(page);
    const frame = await frontendFrame(page);
    await frame.evaluate(() => (window as any).site.reportError(new Error("e2e: reported after ready")));
    await expect
      .poll(() => reports.find((r) => r.kind === "frontend-error"))
      .toMatchObject({ frontend: DEFAULT_REF, message: expect.stringContaining("e2e: reported after ready"), stack: expect.stringContaining("Error") });
    await page.waitForTimeout(300);
    expect(picks).toEqual([""]);
    await expect(page.locator("html")).toHaveClass(/fe-live/);
  });
});

test("a failing front end is reported to the observer before the fallback", async ({ page }) => {
  requireRotation("builtin/e2e-broken");
  const { reports } = await captureReports(page);
  await page.goto("/");
  await waitLive(page, SETTLE_TIMEOUT);
  expect(decodeToken(tokenFromURL((await page.locator("iframe").getAttribute("src"))!)).r).toBe(DEFAULT_REF);
  await expect
    .poll(() => reports.find((r) => r.kind === "frontend-error" && r.frontend === "builtin/e2e-broken"))
    .toMatchObject({ message: expect.stringContaining("e2e-broken: deliberate failure on load") });
});
