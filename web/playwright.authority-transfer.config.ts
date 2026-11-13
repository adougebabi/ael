import { defineConfig } from "@playwright/test";
const output = process.env.AEL_UI_OUT || "/tmp/ael-authority-transfer-browser";
export default defineConfig({
  testDir: "./tests", testMatch: "authority-transfer.spec.ts", workers: 1, timeout: 120000,
  use: { actionTimeout: 15000, viewport: { width: 1440, height: 1000 }, screenshot: "only-on-failure", trace: "retain-on-failure" },
  outputDir: output,
  reporter: [["list"], ["json", { outputFile: output + "/report.json" }]],
});
