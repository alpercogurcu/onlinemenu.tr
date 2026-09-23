import { type APIRequestContext, type Page, expect } from "@playwright/test"

export const API_URL = process.env.E2E_API_URL ?? "http://localhost:8081"

// Seeded by backend/deploy/dev-seed.sql (dev login, APP_ENV=dev). Every role
// is branch-scoped to "Ana Şube" except the chain-wide manager.
export const USERS = {
  manager: "admin@onlinemenu.tr",
  shift: "shift@dev.onlinemenu.tr",
  cashier: "kasiyer@dev.onlinemenu.tr",
  waiter: "garson@dev.onlinemenu.tr",
  kitchen: "mutfak@dev.onlinemenu.tr",
} as const

export const BRANCH_ID = "bbbbbbbb-0000-0000-0000-000000000001"
export const PRODUCT_ID = "dddddddd-0000-0000-0000-000000000201"

// POST /pos/orders re-prices every line against the catalog and refuses a
// mismatch with 422 price_mismatch (docs/pos-ux-spec.md bulgu #14), so a spec
// can no longer invent an amount — it picks a seeded product and uses ITS
// price. Prices are kuruş and mirror backend/deploy/dev-seed.sql.
export const PRODUCTS = {
  adana: { id: "dddddddd-0000-0000-0000-000000000201", name: "Adana Kebap", price: 32_000 },
  tavuk: { id: "dddddddd-0000-0000-0000-000000000202", name: "Tavuk Şiş", price: 28_000 },
  lahmacun: { id: "dddddddd-0000-0000-0000-000000000203", name: "Lahmacun", price: 9_000 },
  corba: { id: "dddddddd-0000-0000-0000-000000000204", name: "Mercimek Çorba", price: 7_500 },
  ayran: { id: "dddddddd-0000-0000-0000-000000000205", name: "Ayran", price: 4_000 },
} as const

export type SeededProduct = (typeof PRODUCTS)[keyof typeof PRODUCTS]

// Each role lands on its own home after sign-in (lib/route-permissions.ts
// homeRouteFor): manager/shift -> "/", cashier -> /pos/checks, waiter ->
// /pos/tables, kitchen -> /pos/kitchen.
export const HOME = {
  [USERS.manager]: "/",
  [USERS.shift]: "/",
  [USERS.cashier]: "/pos/checks",
  [USERS.waiter]: "/pos/tables",
  [USERS.kitchen]: "/pos/kitchen",
} as Record<string, string>

export async function loginAs(page: Page, email: string) {
  await page.goto("/login")
  await page.getByLabel("E-posta").fill(email)
  await page.getByLabel("Şifre").fill("dev")
  await page.getByRole("button", { name: "Giriş Yap (dev)" }).click()
  await page.waitForURL((url) => !url.pathname.startsWith("/login"))
  const home = HOME[email]
  if (home) await page.waitForURL((url) => url.pathname === home)
  await expect(page.locator('[data-sidebar="group-label"]').first()).toBeVisible()
}

/** Sidebar link targets, in menu order. */
export async function sidebarLinks(page: Page): Promise<string[]> {
  return page
    .locator('a[data-sidebar="menu-button"][href]')
    .evaluateAll((els) => els.map((el) => el.getAttribute("href") ?? ""))
}

// The CTX token lives in memory only, so a hard navigation drops the
// session — always move between screens through the SPA router.
//
// window.next.router is undocumented ("Exists for debugging purposes. Don't
// use in application code", next/dist/client/components/app-router-instance.js)
// and could disappear on a Next upgrade; assert it exists first so that
// failure names itself instead of surfacing as an opaque TypeError deep in
// page.evaluate.
export async function gotoSpa(page: Page, path: string) {
  const hasRouter = await page.evaluate(
    () => Boolean((window as unknown as { next?: { router?: unknown } }).next?.router),
  )
  expect(hasRouter, "window.next.router is unavailable — Next.js internals may have changed").toBeTruthy()

  await page.evaluate((p) => {
    ;(window as unknown as { next: { router: { push: (p: string) => void } } }).next.router.push(p)
  }, path)
  await page.waitForURL((url) => url.pathname === path)
}

export async function sidebarGroups(page: Page): Promise<string[]> {
  return page.locator('[data-sidebar="group-label"]').allTextContents()
}

export async function devToken(request: APIRequestContext, email: string) {
  const res = await request.post(`${API_URL}/dev/login`, { data: { email } })
  expect(res.ok(), `dev login for ${email}`).toBeTruthy()
  const body = (await res.json()) as { token: string; tenant_id: string }
  return { token: body.token, tenantId: body.tenant_id }
}

export function errorToast(page: Page) {
  return page.locator('[data-sonner-toast][data-type="error"]')
}
