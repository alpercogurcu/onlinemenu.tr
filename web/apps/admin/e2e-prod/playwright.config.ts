// Production acceptance suite — SEPARATE from ../playwright.config.ts on
// purpose. That config owns the dev suite (testDir ./e2e, localhost, dev
// login) and must not be touched; this one points at the live pilot stack.
//
//   set -a; . deploy/.env.diverserver.local; set +a
//   E2E_PROD=1 pnpm --filter @onlinemenu/admin exec \
//     playwright test -c e2e-prod/playwright.config.ts
//
// Without E2E_PROD=1 globalSetup refuses to start, and fixtures/prod.ts throws
// on import as a second line of defence.

import { defineConfig } from "@playwright/test"

export default defineConfig({
  testDir: ".",
  globalSetup: "./global-setup.ts",
  timeout: 180_000,
  expect: { timeout: 30_000 },
  reporter: "list",
  // One shared production tenant: parallel runs would race the kitchen board,
  // the cash drawer and the table plan.
  fullyParallel: false,
  workers: 1,
  retries: 0,
  use: {
    baseURL: process.env.E2E_PROD_BASE_URL ?? "https://pos.diverstreetfood.com",
    // Playwright'ın navigationTimeout varsayılanı 0'dır (sınırsız); bir
    // gezinme takılırsa test, hatayı bildirmek yerine kendi zaman aşımına
    // kadar sessizce bekler. Prod koşusunda ikisi de açıkça sınırlanır.
    actionTimeout: 30_000,
    navigationTimeout: 45_000,
    trace: "retain-on-failure",
    locale: "tr-TR",
    viewport: { width: 1366, height: 900 },
  },
})
