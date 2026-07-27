import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/browser",
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 2 : 0,
  reporter: "line",
  use: {
    baseURL: "http://127.0.0.1:4173",
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
        viewport: { width: 393, height: 851 },
      },
    },
  ],
  webServer: {
    command: "pnpm start --host 127.0.0.1 --port 4173",
    url: "http://127.0.0.1:4173",
    reuseExistingServer: !process.env.CI,
  },
});
