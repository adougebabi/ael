import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./tests", testMatch: "remote-dashboard.spec.ts", workers: 1, timeout: 150000,
  use: { actionTimeout: 15000, timezoneId: "Asia/Shanghai", viewport: { width: 1440, height: 1000 }, screenshot: "only-on-failure", trace: "retain-on-failure" },
  outputDir: process.env.AEL_UI_OUT || "/tmp/ael-remote-dashboard-browser",
  reporter: [["list"], ["json", { outputFile: (process.env.AEL_UI_OUT || "/tmp/ael-remote-dashboard-browser") + "/report.json" }]],
});
