import { defineConfig } from "@playwright/test"

// Role-based end-to-end suite. The stack is started outside of Playwright
// (dev compose + API + `task web:dev`) — there is no webServer block on
// purpose: the API needs its own env and Docker, which a test runner should
// not own. Point E2E_BASE_URL / E2E_API_URL at a different port if needed.
export default defineConfig({
  testDir: "./e2e",
  timeout: 90_000,
  expect: { timeout: 15_000 },
  reporter: "list",
  // Specs share one dev tenant and one kitchen stream; running them in
  // parallel would make ticket counts and product lists flaky.
  fullyParallel: false,
  workers: 1,
  retries: 0,
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://localhost:3000",
    trace: "retain-on-failure",
    locale: "tr-TR",
    viewport: { width: 1366, height: 900 },
  },
})
