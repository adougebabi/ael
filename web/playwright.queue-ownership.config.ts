import { defineConfig } from "@playwright/test";
// Queue ownership UI over scripts/queue_ownership_acceptance.py --keep.
// AEL_QUEUE_FIXTURE: manifest.json of a freshly seeded fixture (tests mutate it).
const out = process.env.AEL_UI_OUT || "/tmp/ael-queue-ownership-browser";
export default defineConfig({
  testDir: "./tests", testMatch: "queue-ownership.spec.ts", workers: 1, fullyParallel: false, timeout: 120000,
  use: { actionTimeout: 15000, timezoneId: "Asia/Shanghai", viewport: { width: 1440, height: 900 }, screenshot: "only-on-failure", trace: "retain-on-failure", launchOptions: process.env.AEL_CHROMIUM ? { executablePath: process.env.AEL_CHROMIUM } : {} },
  outputDir: out,
  reporter: [["list"]],
});
