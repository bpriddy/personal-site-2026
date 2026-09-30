import { defineConfig } from "@playwright/test";

// Chromium launch flags, verified on the dev box (GTX 1070, no root) with
// Playwright 1.63 / Chrome for Testing 153 via tests/webgpu-feasibility.spec.ts
// and scripts/probe-flags.mjs.
//
// - Real GPU works only in chrome-headless-shell (the "new headless" full
//   Chromium gets a null adapter with the same flags), hence the pinned channel.
// - SwiftShader (CPU Vulkan) works in both; use it where there's no GPU
//   (E2E_WEBGPU_ADAPTER=swiftshader). Plain --use-webgpu-adapter=swiftshader
//   without --use-vulkan=swiftshader gets a device whose buffers die on mapAsync.
// - --disable-webgpu leaves navigator.gpu defined but requestAdapter() resolves
//   null, which is the "no WebGPU" case front ends must handle.
const WEBGPU_FLAGS = {
  gpu: ["--enable-unsafe-webgpu", "--enable-features=Vulkan", "--use-angle=vulkan", "--ignore-gpu-blocklist"],
  swiftshader: [
    "--enable-unsafe-webgpu",
    "--enable-unsafe-swiftshader",
    "--enable-features=Vulkan",
    "--use-vulkan=swiftshader",
    "--use-angle=swiftshader",
    "--use-webgpu-adapter=swiftshader",
  ],
} as const;
const NO_WEBGPU_FLAGS = ["--disable-webgpu"];

const adapter = (process.env.E2E_WEBGPU_ADAPTER ?? "gpu") as keyof typeof WEBGPU_FLAGS;
if (!(adapter in WEBGPU_FLAGS)) throw new Error(`E2E_WEBGPU_ADAPTER must be one of ${Object.keys(WEBGPU_FLAGS)}`);

const MAIN_ORIGIN = process.env.E2E_MAIN_ORIGIN ?? "http://localhost:8080";

export default defineConfig({
  testDir: "./tests",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  // the servers under test hold shared in-memory state and one GPU
  fullyParallel: false,
  workers: Number(process.env.E2E_WORKERS ?? 2),
  retries: 0,
  // @slow tests (token expiry waits) run only with E2E_SLOW=1 (run.sh --slow)
  grepInvert: process.env.E2E_SLOW ? undefined : /@slow/,
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    browserName: "chromium",
    channel: "chromium-headless-shell",
    headless: true,
    baseURL: MAIN_ORIGIN,
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "webgpu",
      testIgnore: /no-webgpu\.spec\.ts/,
      use: { launchOptions: { args: [...WEBGPU_FLAGS[adapter]] } },
    },
    {
      name: "no-webgpu",
      testMatch: /(no-webgpu|webgpu-feasibility)\.spec\.ts/,
      use: { launchOptions: { args: NO_WEBGPU_FLAGS } },
    },
  ],
});
