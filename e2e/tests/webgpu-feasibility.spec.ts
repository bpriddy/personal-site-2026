// Proves the browser can (webgpu project) or cannot (no-webgpu project) run
// WebGPU, independent of the system under test: a throwaway static server serves
// fixtures/webgpu-probe.html, which renders a frame and runs a compute pass.
// It also runs the probe in a cross-site sandboxed iframe (localhost parent,
// 127.0.0.1 child, sandbox="allow-scripts"), the shape the real site uses.
import { test, expect, type Frame, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import http from "node:http";
import type { AddressInfo } from "node:net";

type Probe = {
  hasGpu?: boolean;
  adapter?: { vendor: string; architecture: string; isFallbackAdapter: boolean } | null;
  device?: boolean;
  render?: { ok: boolean; pixel: number[]; format: string } | null;
  compute?: { ok: boolean } | null;
  error?: string | null;
};

let server: http.Server;
let port: number;

test.beforeAll(async () => {
  const probe = await readFile(new URL("../fixtures/webgpu-probe.html", import.meta.url));
  server = http.createServer((req, res) => {
    res.setHeader("content-type", "text/html; charset=utf-8");
    if (req.url === "/parent.html") {
      res.end(`<!doctype html><iframe sandbox="allow-scripts" src="http://127.0.0.1:${port}/probe.html"></iframe>`);
    } else if (req.url === "/probe.html") {
      res.end(probe);
    } else {
      res.statusCode = 404;
      res.end();
    }
  });
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  port = (server.address() as AddressInfo).port;
});

test.afterAll(() => new Promise<void>((r) => server.close(() => r())));

async function runProbe(where: Page | Frame): Promise<Probe> {
  const h = await where.waitForFunction(() => (window as any).__probe, null, { timeout: 30_000 });
  return (await h.jsonValue()) as Probe;
}

function check(probe: Probe, project: string) {
  test.info().annotations.push({ type: "probe", description: JSON.stringify(probe) });
  if (project === "no-webgpu") {
    // navigator.gpu may exist, but there must be no adapter
    expect(probe.error ?? null).toBeNull();
    expect(probe.adapter ?? null).toBeNull();
    return;
  }
  expect(probe.error ?? null).toBeNull();
  expect(probe.adapter, "requestAdapter() returned null").toBeTruthy();
  expect(probe.device).toBe(true);
  expect(probe.render?.ok, `rendered pixel ${JSON.stringify(probe.render)}`).toBe(true);
  expect(probe.compute?.ok).toBe(true);
  if ((process.env.E2E_WEBGPU_ADAPTER ?? "gpu") === "gpu") {
    expect(probe.adapter!.isFallbackAdapter, "expected the real GPU, got a software adapter").toBe(false);
  }
}

test("top-level page", async ({ page }, info) => {
  await page.goto(`http://127.0.0.1:${port}/probe.html`);
  check(await runProbe(page), info.project.name);
});

test("cross-site sandboxed iframe", async ({ page }, info) => {
  await page.goto(`http://localhost:${port}/parent.html`);
  const frame = await (await page.locator("iframe").elementHandle())!.contentFrame();
  check(await runProbe(frame!), info.project.name);
});
