import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  globalSetup: "./e2e/global-setup.ts",
  fullyParallel: true,
  workers: 3,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    { name: "firefox", use: { ...devices["Desktop Firefox"] } },
    { name: "webkit", use: { ...devices["Desktop Safari"] } },
  ],
  testDir: "./e2e",
  use: { trace: "retain-on-failure" },
});
