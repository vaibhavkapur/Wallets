import { defineConfig, devices } from "@playwright/test";
export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  workers: 1,
  timeout: 40000,
  expect: { timeout: 15000 },
  use: {
    baseURL: "http://127.0.0.1:8099",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "bash ../scripts/e2e-server.sh",
    url: "http://127.0.0.1:8099/healthz",
    reuseExistingServer: false,
    timeout: 120000,
  },
});
