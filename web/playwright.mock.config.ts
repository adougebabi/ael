import { defineConfig } from "@playwright/test";
// Front-end scenario checks against `npm run dev:mock` (captured API
// responses, no AEL Server). They prove page rendering and interaction only;
// real-service behavior stays covered by the fixture-based suites.
const port = Number(process.env.AEL_MOCK_PORT || 5199);
const out = process.env.AEL_UI_OUT || "/tmp/ael-mock-browser";
export default defineConfig({
  testDir: "./tests", testMatch: "mock-scenarios.spec.ts", workers: 1, timeout: 60000,
  use: { baseURL: `http://localhost:${port}`, viewport: { width: 1440, height: 900 }, timezoneId: "Asia/Shanghai", screenshot: "only-on-failure", launchOptions: process.env.AEL_CHROMIUM ? { executablePath: process.env.AEL_CHROMIUM } : {} },
  webServer: { command: `npx vite --mode mock --port ${port} --strictPort`, url: `http://localhost:${port}`, reuseExistingServer: true, timeout: 60000 },
  outputDir: out,
  reporter: [["list"]],
});
