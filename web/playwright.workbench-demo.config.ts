import { defineConfig } from "@playwright/test";
// Workbench restoration checks against scripts/workbench_demo_fixture.py.
// AEL_WB_FIXTURE: manifest.json; AEL_WB_URL optionally points at a Vite dev
// server proxying the same fixture. Tests mutate the fixture, so run serially
// on a freshly seeded fixture.
const out = process.env.AEL_UI_OUT || "/tmp/ael-workbench-demo-browser";
export default defineConfig({
  testDir: "./tests", testMatch: "workbench-demo.spec.ts", workers: 1, fullyParallel: false, timeout: 120000,
  use: { actionTimeout: 15000, timezoneId: "Asia/Shanghai", viewport: { width: 1440, height: 900 }, screenshot: "only-on-failure", trace: "retain-on-failure", launchOptions: process.env.AEL_CHROMIUM ? { executablePath: process.env.AEL_CHROMIUM } : {} },
  outputDir: out,
  reporter: [["list"], ["json", { outputFile: out + "/report.json" }]],
});
