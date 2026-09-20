// (f) Yönetim paneli — canlı Keycloak SSO ile giriş ve üç ekranın doğrulaması.
// Prod'da /dev/login yok; giriş gerçek Keycloak formundan yapılır.
//
// TEK giriş, sonra SPA yönlendirmesi: panelin CTX token'ı yalnız bellekte
// yaşar (src/lib/session-restore.ts) ve her test için /login'e sert gidiş,
// Keycloak SSO çerezi zaten varken parola formunu hiç göstermeyip testi
// kilitler (2026-09-20 koşusunda görüldü). Bu yüzden oturum beforeAll'da bir
// kez açılır ve tüm testler aynı sayfayı paylaşır — e2e/kds.spec.ts deseni.

import { type Page, expect, test } from "@playwright/test"

import { ACCOUNTS, TAG, gotoSpa, loginAdmin } from "./fixtures/prod"

test.describe.configure({ mode: "serial" })

test.describe("(f) yönetim paneli", () => {
  let page: Page

  test.beforeAll(async ({ browser }) => {
    const context = await browser.newContext()
    page = await context.newPage()
    await loginAdmin(page, ACCOUNTS.manager())
  })

  test.afterAll(async () => {
    await page.context().close()
  })

  test("test yöneticisi giriş yapar ve panele ulaşır", async () => {
    await expect(page.locator('[data-sidebar="group-label"]').first()).toBeVisible()
  })

  test("Şube Fiyatları sayfasında İzmit 10 'Şube fiyatı' rozeti gösterir", async () => {
    await gotoSpa(page, "/catalog/branch-pricing")
    await expect(page.getByRole("heading", { name: "Şube Fiyatları" })).toBeVisible()

    // Süzgecin erişilebilir adı catalog.branchPricing.branch = "Şube";
    // rozetin metni catalog.branchPricing.state.branch = "Şube fiyatı".
    // İkisi ayrı anahtardır, karıştırılmamalı. Süzgeç ada göre seçilir
    // (sayfada başka bir combobox yok ama sıraya güvenmek kırılgandır).
    const branchFilter = page.getByRole("combobox", { name: "Şube" })
    const badges = page.locator("span[data-slot=badge]", { hasText: "Şube fiyatı" })

    await branchFilter.selectOption({ label: "İzmit" })
    await expect.poll(() => badges.count(), { timeout: 30_000 }).toBe(10)

    // Kontrol: Serdivan'da şubeye özel fiyat yok.
    await branchFilter.selectOption({ label: "Serdivan" })
    await expect.poll(() => badges.count(), { timeout: 30_000 }).toBe(0)
  })

  test("Kullanıcılar sayfasında test personeli şube etiketiyle listelenir", async () => {
    await gotoSpa(page, "/settings/users")
    // Sayfa 25'erli sayfalanır (settings/users/page.tsx PAGE_SIZE); 10 üyeyle
    // tek sayfa, yine de sayım yerine üye özetini ve tekil satırları doğrula.
    await expect(page.getByTestId("members-summary")).toContainText("üye")

    const rows = page.locator("table tbody tr")
    await expect.poll(() => rows.filter({ hasText: TAG }).count(), { timeout: 30_000 }).toBeGreaterThanOrEqual(8)

    // Şube kapsamlı roller şube adıyla görünür (SEC-005).
    await expect(rows.filter({ hasText: "admin+kasiyer.izmit@diverstreetfood.com" })).toContainText("İzmit")
    await expect(rows.filter({ hasText: "admin+kasiyer.serdivan@diverstreetfood.com" })).toContainText("Serdivan")
    await expect(rows.filter({ hasText: "admin+mutfak.izmit@diverstreetfood.com" })).toContainText("Mutfak")
  })

  test("Şubeler listesi 5 şube gösterir", async () => {
    await gotoSpa(page, "/settings/branches")
    const rows = page.locator("table tbody tr")
    await expect.poll(() => rows.count(), { timeout: 30_000 }).toBe(5)
    for (const name of ["Serdivan", "İzmit", "Adapazarı", "Kırkpınar", "İmalat Merkezi"]) {
      await expect(rows.filter({ hasText: name }).first()).toBeVisible()
    }
  })
})
