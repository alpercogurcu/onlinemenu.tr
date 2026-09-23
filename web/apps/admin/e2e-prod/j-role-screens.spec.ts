// (j) Rol ekranları taraması (2026-09-23) — garson ekranındaki kalite düzeyinin
// kasiyer, mutfak ve yöneticiye taşınması. Her bulgu için bir kabul adımı:
//   - mutfak kartı kalem notunu (seçenekler) ve sipariş notunu (alerji) gösterir;
//   - kasiyer ödenmemiş adisyonda "Kapat"ı kilitli görür ve nedenini okur,
//     "İptal" onay ister; bekleyen siparişi adisyondan kabul eder;
//   - kasiyerin masalarında QR düğmesi yoktur, dolu masa adisyona bağlanır;
//   - yönetici seçenek grubunu grubun kendi sayfasından ürüne atar
//     ("Patates Seçimi atanamıyor" şikâyeti);
//   - üst çubuktaki bölüm adı (POS/Katalog) 404'e giden bir bağlantı değildir.
//
// Veri: yalnız PRODTEST ön ekli adisyon/ürün/grup; gerçek "Patates Seçimi"ne
// dokunulmaz. Her şey finally'de geri alınır (sipariş servis → adisyon iptal →
// masa boş; ürün menüden çıkarılır ve silinir; grup silinir).

import { type APIRequestContext, type Browser, type Page, expect, test } from "@playwright/test"

import {
  ACCOUNTS,
  type Account,
  type Principal,
  RUN,
  allTables,
  cleanupCheck,
  gotoSpa,
  json,
  line,
  loginAdmin,
  principal,
  productByName,
  releaseTable,
} from "./fixtures/prod"

test.describe.configure({ mode: "serial" })

const SMASH = "American Smash Burger"
const OPTION_NOTE = `${RUN} Acılı | Soğansız`
const ORDER_NOTE = `${RUN} alerji: fıstık`

async function openAs(browser: Browser, acct: Account): Promise<Page> {
  // Separate context per role: a shared Keycloak SSO cookie would log the
  // second role in as the first.
  const context = await browser.newContext({ viewport: { width: 1366, height: 768 } })
  const page = await context.newPage()
  await loginAdmin(page, acct)
  return page
}

test.describe("(j) rol ekranları", () => {
  let shared: APIRequestContext
  let manager: Principal
  let waiter: Principal
  let cashier: Principal
  let kitchen: Principal
  let branchId: string
  let tableId: string | undefined
  let checkId: string | undefined
  let pendingOrderId: string | undefined

  test.beforeAll(async ({ playwright }) => {
    shared = await playwright.request.newContext()
    manager = await principal(shared, ACCOUNTS.manager())
    waiter = await principal(shared, ACCOUNTS.waiterSerdivan())
    cashier = await principal(shared, ACCOUNTS.cashierSerdivan())
    kitchen = await principal(shared, ACCOUNTS.kitchenSerdivan())
    branchId = waiter.ctx.branch_id as string

    const table = (await allTables(manager.api, branchId)).find((t) => t.status === "empty")
    expect(table, "Serdivan'da boş masa olmalı").toBeTruthy()
    tableId = (table as { id: string }).id

    const check = await json<{ id: string }>(
      await waiter.api.post("/api/v1/pos/checks", { branch_id: branchId, table_id: tableId, pax: 2 }),
      201,
    )
    checkId = check.id
    const smash = await productByName(waiter.api, SMASH, branchId)
    // Waiter orders wait for the counter (pending) — exactly what the kitchen
    // card and the cashier's accept button are about.
    const res = await waiter.api.postNew("/api/v1/pos/orders", {
      branch_id: branchId,
      check_id: checkId,
      order_channel: "dine_in",
      note: ORDER_NOTE,
      items: [{ ...line(smash, 2), note: OPTION_NOTE }],
    })
    pendingOrderId = (await json<{ id: string }>(res, 201)).id
  })

  test.afterAll(async () => {
    if (checkId) await cleanupCheck(cashier.api, kitchen.api, checkId)
    if (tableId) await releaseTable(manager.api, branchId, tableId)
    await shared.dispose()
  })

  test("mutfak kartı seçenek notunu ve sipariş notunu gösterir", async ({ browser }) => {
    const page = await openAs(browser, ACCOUNTS.kitchenSerdivan())
    try {
      await page.waitForURL((url) => url.pathname === "/pos/kitchen")
      const card = page.locator("[data-kds-root] [data-slot=card]").filter({ hasText: ORDER_NOTE }).first()
      await expect(card).toBeVisible()
      await expect(card.getByText(OPTION_NOTE)).toBeVisible()
      await expect(card.getByText("Kasa onayı bekleniyor")).toBeVisible()
    } finally {
      await page.context().close()
    }
  })

  test("kasiyer: masada QR yok, dolu masa adisyona bağlanır; ödenmemiş adisyon kilitli, iptal onaylı, sipariş kabul edilir", async ({
    browser,
  }) => {
    const page = await openAs(browser, ACCOUNTS.cashierSerdivan())
    try {
      await gotoSpa(page, "/pos/tables")
      await expect(page.getByRole("link", { name: /adisyonunu aç$/ }).first()).toBeVisible()
      await expect(page.getByRole("button", { name: /^QR$/ })).toHaveCount(0)
      // Section crumb is text, not a link to the non-existent /pos page.
      await expect(page.getByRole("link", { name: "POS", exact: true })).toHaveCount(0)

      await gotoSpa(page, "/pos/checks")
      await expect(page.getByRole("button", { name: /^Açık \(\d+\)$/ })).toHaveAttribute("aria-pressed", "true")

      await gotoSpa(page, `/pos/checks/${checkId}`)
      await expect(page.getByRole("button", { name: "Kapat" })).toBeDisabled()
      await expect(page.getByText(/POS uygulamasından alın/)).toBeVisible()

      await page.getByRole("button", { name: "İptal" }).click()
      const dialog = page.getByRole("alertdialog")
      await expect(dialog).toBeVisible()
      await dialog.getByRole("button", { name: "Vazgeç" }).click()
      await expect(dialog).toHaveCount(0)

      await page.getByRole("button", { name: "Siparişi kabul et" }).click()
      await expect
        .poll(async () => {
          const orders = await json<{ id: string; status: string }[]>(
            await cashier.api.get(`/api/v1/pos/checks/${checkId}/orders`),
            200,
          )
          return orders.find((o) => o.id === pendingOrderId)?.status
        })
        .toBe("accepted")
    } finally {
      await page.context().close()
    }
  })

  test("yönetici seçenek grubunu grubun sayfasından ürüne atar ve çıkarır", async ({ browser }) => {
    // Everything that creates state sits inside the try: a Keycloak timeout
    // must not leave a PRODTEST group/product behind on the live tenant.
    let groupId: string | undefined
    let productId: string | undefined
    let page: Page | undefined
    try {
      groupId = (
        await json<{ id: string }>(
          await manager.api.post("/api/v1/catalog/modifier-groups", {
            name: `${RUN} Grup`,
            selection_type: "single",
            min_selections: 0,
            max_selections: 1,
            is_required: false,
          }),
          201,
        )
      ).id
      productId = (
        await json<{ id: string }>(
          await manager.api.post("/api/v1/catalog/products", {
            name: `${RUN} Ürün`,
            price_amount: 100,
            currency: "TRY",
            unit: "C62",
            tax_rate_bps: 1000,
          }),
          201,
        )
      ).id
      // A new product joins the tenant's only active menu automatically
      // (ProductService.addToSoleActiveMenu) — take it straight back out so
      // no guest sees a test product.
      const menus = await json<{ id: string; is_active: boolean }[]>(
        await manager.api.get("/api/v1/catalog/menus"),
        200,
      )
      for (const menu of menus.filter((m) => m.is_active)) {
        await manager.api.del(`/api/v1/catalog/menus/${menu.id}/items/${productId}`)
      }

      // An unused group answers `null`, not [] (Go nil slice) — the admin
      // hook folds it with `?? []`, so does this probe.
      const assignedTo = async () =>
        (await json<string[] | null>(await manager.api.get(`/api/v1/catalog/modifier-groups/${groupId}/products`), 200)) ??
        []

      page = await openAs(browser, ACCOUNTS.manager())
      await gotoSpa(page, `/catalog/modifiers/${groupId}`)
      await page.getByRole("button", { name: "Ürüne ekle" }).click()
      await page.getByPlaceholder("Ürün ara").fill(RUN)
      await page.getByRole("option", { name: `${RUN} Ürün` }).click()
      await expect.poll(assignedTo).toEqual([productId])

      await page.getByRole("button", { name: `${RUN} Ürün — gruptan çıkar` }).click()
      await expect.poll(assignedTo).toEqual([])
    } finally {
      await page?.context().close()
      if (productId && groupId) await manager.api.del(`/api/v1/catalog/products/${productId}/modifier-groups/${groupId}`)
      if (productId) await manager.api.del(`/api/v1/catalog/products/${productId}`)
      if (groupId) await manager.api.del(`/api/v1/catalog/modifier-groups/${groupId}`)
    }
  })
})
