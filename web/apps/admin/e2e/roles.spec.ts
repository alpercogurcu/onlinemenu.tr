import { randomUUID } from "node:crypto"

import { type Page, expect, test } from "@playwright/test"

import { API_URL, BRANCH_ID, PRODUCTS, USERS, devToken, gotoSpa, loginAs, sidebarLinks } from "./fixtures/auth"

const NO_REPORT = "Bu rapor için Shift Müdürü yetkisi gerekir."

// Reviewed expectation, derived from the generated OPA matrix through
// lib/route-permissions.ts (same table as src/test/route-permissions.test.ts).
// Manager is omitted here: the sidebar still depends on the tenant's enabled
// modules for them, and route-permissions.test.ts pins "manager sees all".
const MENU: Record<string, string[]> = {
  [USERS.shift]: ["/", "/pos/tables", "/pos/checks", "/pos/kitchen", "/payment/payments"],
  [USERS.cashier]: ["/pos/tables", "/pos/checks", "/pos/kitchen"],
  // One floor plan: the waiter's "Masalar" opens the order screen's plan.
  [USERS.waiter]: ["/pos/order", "/pos/checks"],
  [USERS.kitchen]: ["/pos/tables", "/pos/kitchen"],
}

// Screens each role must NOT reach — every one must render the access-denied
// state and must not cause a single 4xx from the API (the page never mounts).
const FORBIDDEN: Record<string, string[]> = {
  [USERS.shift]: ["/catalog/products", "/catalog/branch-pricing", "/settings/users", "/inventory/warehouses"],
  [USERS.cashier]: ["/catalog/products", "/catalog/products/new", "/payment/payments", "/settings/branches"],
  [USERS.waiter]: [
    "/catalog/products",
    "/catalog/products/new",
    `/catalog/products/${PRODUCTS.adana.id}`,
    "/catalog/categories",
    "/pos/kitchen",
    "/payment/payments",
    "/settings/users",
    "/inventory/stock-levels",
  ],
  [USERS.kitchen]: ["/pos/checks", "/catalog/products", "/settings/users", "/payment/payments"],
}

// Records every 4xx the API answers while `run` executes.
async function apiErrorsDuring(page: Page, run: () => Promise<void>): Promise<string[]> {
  const errors: string[] = []
  const listener = (res: { status(): number; url(): string; request(): { method(): string } }) => {
    const url = res.url()
    const isApi = url.startsWith(API_URL) || url.includes("/api/core") || url.includes("/api/v1/")
    if (isApi && res.status() >= 400 && res.status() < 500) {
      errors.push(`${res.status()} ${res.request().method()} ${url}`)
    }
  }
  page.on("response", listener)
  try {
    await run()
    // Let the screen's queries settle before judging: every data-backed page
    // renders <Skeleton> while loading, so "no skeleton left" is the settled
    // state. Not "networkidle" — the kitchen display keeps its live stream
    // open, so the network never goes idle and that wait never returned.
    await expect(page.locator('[data-slot="skeleton"]')).toHaveCount(0)
  } finally {
    page.off("response", listener)
  }
  return errors
}

test.describe("rol bazlı görünürlük", () => {
  for (const [email, expected] of Object.entries(MENU)) {
    test(`${email}: menü yalnız izinli ekranlar, yasak rotalar erişim-yok ve ağda 4xx yok`, async ({ page }) => {
      const loginErrors = await apiErrorsDuring(page, () => loginAs(page, email))
      expect(loginErrors, "girişte/ana sayfada 4xx").toEqual([])

      expect(await sidebarLinks(page)).toEqual(expected)

      for (const path of FORBIDDEN[email]) {
        const errors = await apiErrorsDuring(page, async () => {
          await gotoSpa(page, path)
          await expect(page.getByTestId("access-denied")).toBeVisible()
        })
        expect(errors, `${path} 4xx üretmemeli`).toEqual([])
      }
    })
  }

  test("yönetici gösterge panelinde raporu görür, tüm bölümler açık", async ({ page }) => {
    await loginAs(page, USERS.manager)
    await expect(page.getByText(NO_REPORT)).toHaveCount(0)
    expect(await page.locator('[data-sidebar="group-label"]').allTextContents()).toEqual([
      "Genel",
      "POS",
      "Katalog",
      "Stok",
      "Ödeme",
      "İşletme",
    ])
  })

  test("mutfak '/' adresine gelince mutfak ekranına yönlendirilir", async ({ page }) => {
    await loginAs(page, USERS.kitchen)
    await gotoSpa(page, "/pos/tables")
    await page.evaluate(() => {
      ;(window as unknown as { next: { router: { push: (p: string) => void } } }).next.router.push("/")
    })
    await page.waitForURL((url) => url.pathname === "/pos/kitchen")
    await expect(page.getByText("Canlı", { exact: true })).toBeVisible()
  })

  test("garson masalarda yönetim/QR kontrolü görmez", async ({ page }) => {
    await loginAs(page, USERS.waiter)
    // No longer in the waiter's menu, but still reachable by URL.
    await gotoSpa(page, "/pos/tables")
    await expect(page.getByText("Masa 1", { exact: true })).toBeVisible()
    await expect(page.getByRole("button", { name: /QR/i })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "Bölge ekle" })).toHaveCount(0)
    await expect(page.getByRole("button", { name: /Masa ekle/i })).toHaveCount(0)
  })

  test("adisyon detayı: garson kalemleri görür, ödeme/kapat yok; kasiyer ödeme özetini ve kapat'ı görür", async ({
    page,
    request,
  }) => {
    const { token } = await devToken(request, USERS.waiter)
    const headers = { Authorization: `Bearer ${token}` }
    const label = `E2E-DETAY-${randomUUID().slice(0, 6)}`
    const opened = await request.post(`${API_URL}/api/v1/pos/checks`, {
      headers,
      data: { branch_id: BRANCH_ID, table_label: label, pax: 3 },
    })
    expect(opened.status(), await opened.text()).toBe(201)
    const checkId = ((await opened.json()) as { id: string }).id
    const order = await request.post(`${API_URL}/api/v1/pos/orders`, {
      headers: { ...headers, "Idempotency-Key": `e2e-roles-${randomUUID()}` },
      data: {
        branch_id: BRANCH_ID,
        check_id: checkId,
        order_channel: "dine_in",
        items: [
          {
            product_id: PRODUCTS.adana.id,
            product_name: PRODUCTS.adana.name,
            product_price_amount: PRODUCTS.adana.price,
            product_currency: "TRY",
            tax_rate_bps: 1000,
            quantity: 2,
            unit_price_amount: PRODUCTS.adana.price,
          },
        ],
      },
    })
    expect(order.status(), await order.text()).toBe(201)

    try {
      await loginAs(page, USERS.waiter)
      const errors = await apiErrorsDuring(page, async () => {
        await gotoSpa(page, "/pos/checks")
        await page.getByRole("link", { name: `"${label}" adisyonunun detayını aç` }).click()
        await page.waitForURL((url) => url.pathname === `/pos/checks/${checkId}`)
        await expect(page.getByRole("heading", { name: label })).toBeVisible()
        await expect(page.getByText(PRODUCTS.adana.name)).toBeVisible()
        await expect(page.getByText("2×")).toBeVisible()
      })
      expect(errors, "garson detayında 4xx (settlement dahil) olmamalı").toEqual([])
      await expect(page.getByText("Kalan")).toHaveCount(0)
      await expect(page.getByRole("button", { name: "Kapat" })).toHaveCount(0)
      await expect(page.getByRole("button", { name: "İptal" })).toHaveCount(0)

      await loginAs(page, USERS.cashier)
      await gotoSpa(page, `/pos/checks/${checkId}`)
      await expect(page.getByText("Kalan", { exact: true })).toBeVisible()
      // The web takes no payment: an unpaid check's "Kapat" is locked and
      // says where the money is taken (2026-09-23 sweep).
      await expect(page.getByRole("button", { name: "Kapat" })).toBeDisabled()
      await expect(page.getByText(/POS uygulamasından alın/)).toBeVisible()
      // "İptal" asks first — dismissing the dialog leaves the check open.
      await page.getByRole("button", { name: "İptal" }).click()
      const dialog = page.getByRole("alertdialog")
      await expect(dialog).toBeVisible()
      await dialog.getByRole("button", { name: "Vazgeç" }).click()
      await expect(dialog).toHaveCount(0)
      await expect(page.getByText("Açık", { exact: true }).first()).toBeVisible()
    } finally {
      const cashier = await devToken(request, USERS.cashier)
      await request.post(`${API_URL}/api/v1/pos/checks/${checkId}/cancel`, {
        headers: { Authorization: `Bearer ${cashier.token}`, "Idempotency-Key": `e2e-roles-${randomUUID()}` },
        data: {},
      })
    }
  })
})
