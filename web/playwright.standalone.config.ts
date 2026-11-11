import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests", testMatch: "standalone-server.spec.ts", workers: 1, timeout: 90000,
  use: { timezoneId: "Asia/Shanghai", viewport: { width: 1440, height: 900 }, screenshot: "only-on-failure", trace: "retain-on-failure" },
  outputDir: process.env.AEL_UI_OUT || "/tmp/ael-standalone-browser",
  reporter: [["list"], ["json", { outputFile: (process.env.AEL_UI_OUT || "/tmp/ael-standalone-browser") + "/report.json" }]],
});
