import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/browser",
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 2 : 0,
  reporter: "line",
  use: {
    serviceWorkers: "block",
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium-desktop",
      grep: /@desktop/,
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "firefox-desktop",
      grep: /@desktop/,
      use: { ...devices["Desktop Firefox"] },
    },
    {
      name: "chromium-mobile",
      grep: /@mobile/,
      use: { ...devices["Pixel 5"] },
    },
    {
      name: "firefox-mobile",
      grep: /@mobile/,
      use: {
        ...devices["Desktop Firefox"],
        hasTouch: true,
        viewport: { width: 393, height: 851 },
      },
    },
  ],
  webServer: {
    command: "pnpm build && node scripts/start-browser-preview.mjs",
    wait: {
      stdout:
        /MEMENTO_BROWSER_URL=(?<PLAYWRIGHT_TEST_BASE_URL>http:\/\/127\.0\.0\.1:\d+)/,
    },
    reuseExistingServer: false,
  },
});
