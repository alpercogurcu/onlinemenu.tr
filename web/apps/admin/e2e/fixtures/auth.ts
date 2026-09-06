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

export async function loginAs(page: Page, email: string) {
  await page.goto("/login")
  await page.getByLabel("E-posta").fill(email)
  await page.getByLabel("Şifre").fill("dev")
  await page.getByRole("button", { name: "Giriş Yap (dev)" }).click()
  await page.waitForURL((url) => url.pathname === "/")
  await expect(page.locator('[data-sidebar="group-label"]').first()).toBeVisible()
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
