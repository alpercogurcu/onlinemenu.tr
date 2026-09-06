import { expect, test } from "@playwright/test"

import { USERS, errorToast, gotoSpa, loginAs, sidebarGroups } from "./fixtures/auth"

const NO_REPORT = "Bu rapor için Shift Müdürü yetkisi gerekir."

test.describe("rol bazlı ekranlar", () => {
  test("yönetici gün sonu raporunu görür", async ({ page }) => {
    await loginAs(page, USERS.manager)
    await expect(page.getByText(NO_REPORT)).toHaveCount(0)
    expect(await sidebarGroups(page)).toEqual(["Genel", "POS", "Katalog", "Stok", "Ödeme", "İşletme"])
  })

  test("shift müdürü raporu görür", async ({ page }) => {
    await loginAs(page, USERS.shift)
    await expect(page.getByText(NO_REPORT)).toHaveCount(0)
  })

  test("kasiyer raporu göremez, ürün ekleyemez", async ({ page }) => {
    await loginAs(page, USERS.cashier)
    await expect(page.getByText(NO_REPORT)).toBeVisible()

    await gotoSpa(page, "/catalog/products")
    await expect(page.getByRole("table")).toBeVisible()
    await page.getByRole("button", { name: "Ürün ekle" }).click()
    await page.waitForURL((url) => url.pathname === "/catalog/products/new")
    await page.locator("#product-name").fill("e2e-kasiyer-urun")
    await page.locator("#product-price").fill("10")
    await page.getByRole("button", { name: "Kaydet" }).click()
    await expect(errorToast(page)).toBeVisible()
    expect(new URL(page.url()).pathname).toBe("/catalog/products/new")
  })

  test("garson masaları görür, QR ve katalog yetkisi yok", async ({ page }) => {
    await loginAs(page, USERS.waiter)
    await gotoSpa(page, "/pos/tables")
    await expect(page.getByText("Masa 1", { exact: true })).toBeVisible()
    // Waiter holds pos.table.read but not storefront.qr.read: the QR action
    // renders, but disabled with an explanatory tooltip.
    const qrButtons = page.getByRole("button", { name: /QR/i })
    await expect(qrButtons.first()).toBeDisabled()
    await expect(page.getByRole("button", { name: /QR/i, disabled: false })).toHaveCount(0)

    await gotoSpa(page, "/catalog/products")
    await expect(page.getByText("Yüklenemedi.")).toBeVisible()
  })

  test("mutfak KDS'yi canlı görür, kullanıcı listesine erişemez", async ({ page }) => {
    await loginAs(page, USERS.kitchen)
    await gotoSpa(page, "/pos/kitchen")
    await expect(page.getByText("Canlı", { exact: true })).toBeVisible()

    await gotoSpa(page, "/pos/tables")
    await expect(page.getByText("Masa 1", { exact: true })).toBeVisible()

    await gotoSpa(page, "/settings/users")
    await expect(page.getByText("Üyeler yüklenemedi")).toBeVisible()
  })
})
