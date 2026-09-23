import { type APIRequestContext, type Page, expect, test } from "@playwright/test"

import { API_URL, BRANCH_ID, PRODUCTS, USERS, devToken, loginAs } from "./fixtures/auth"

// Web order screen (/pos/order), driven the way a waiter uses it on a phone:
// home = table plan → table → option product (panel) + plain product → cart →
// "Mutfağa gönder" → the round is on the adisyon. Teardown goes through the
// counter (a waiter may neither cancel a check nor free an occupied table).

const PHONE = { width: 390, height: 844 }

// dev-seed.sql: Adana Kebap carries "Pişirme Şekli" (required, single; first
// option "Az pişmiş") and "Ekstralar" (optional; "Ekstra sos" +15 TL).
const AZ_PISMIS = "dddddddd-0000-0000-0000-000000000301"
const EKSTRA_SOS = "dddddddd-0000-0000-0000-000000000311"

interface PosTable {
  id: string
  name: string
  status: string
  active_check_id: string | null
}

interface Order {
  id: string
  status: string
  items: { product_id: string; quantity: number; unit_price_amount: number; modifier_ids?: string[] }[]
}

function api(request: APIRequestContext, token: string) {
  const headers = { Authorization: `Bearer ${token}` }
  return {
    get: (path: string) => request.get(`${API_URL}${path}`, { headers }),
    post: (path: string, data?: unknown) => request.post(`${API_URL}${path}`, { headers, data: data ?? {} }),
  }
}

async function tables(manager: ReturnType<typeof api>): Promise<PosTable[]> {
  const res = await manager.get(`/api/v1/pos/tables?branch_id=${BRANCH_ID}`)
  expect(res.status(), await res.text()).toBe(200)
  const plan = (await res.json()) as { tables: PosTable[] }[]
  return plan.flatMap((zone) => zone.tables)
}

// Closing/cancelling a check moves the table to `cleaning` synchronously in the
// same transaction; the retry only guards a write made while the check was
// still open (same reasoning as e2e-prod releaseTable).
async function releaseTable(manager: ReturnType<typeof api>, tableId: string) {
  for (let attempt = 0; attempt < 6; attempt++) {
    await manager.post(`/api/v1/pos/tables/${tableId}/status`, { status: "empty" })
    const table = (await tables(manager)).find((t) => t.id === tableId)
    if (table?.status === "empty") return
    await new Promise((resolve) => setTimeout(resolve, 1_000))
  }
}

const lira = new Intl.NumberFormat("tr-TR", { style: "currency", currency: "TRY" })
const escapeRe = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")

// The dev tenant accumulates look-alikes from other specs and manual testing
// (a second "Adana Kebap", two "Ana Yemekler" categories), so a tile is found
// by name AND the seeded price, walking the category chips until it shows up.
async function tapTile(page: Page, product: { name: string; price: number }) {
  const tile = page.getByRole("button", {
    name: new RegExp(`^${escapeRe(product.name)}, ${escapeRe(lira.format(product.price / 100))}`),
  })
  const tabs = page.getByRole("tablist", { name: "Kategoriler" }).getByRole("tab")
  await expect(tabs.first()).toBeVisible()
  for (let i = 0; (await tile.count()) === 0 && i < (await tabs.count()); i++) {
    await tabs.nth(i).click()
  }
  await tile.click()
}

/** Taps a free table on the order screen's plan, walking the zone tabs if there are several. */
async function tapFreeTable(page: Page, tableName: string) {
  const tile = page.getByRole("button", { name: `${tableName}, Boş`, exact: true })
  const zones = page.getByRole("tablist", { name: "Bölgeler" }).getByRole("tab")
  await expect(page.getByTestId("table-tile").first()).toBeVisible()
  for (let i = 0; (await tile.count()) === 0 && i < (await zones.count()); i++) {
    await zones.nth(i).click()
  }
  await tile.click()
}

test.describe("garson web sipariş ekranı", () => {
  let shared: APIRequestContext
  let manager: ReturnType<typeof api>
  let table: PosTable
  let checkId: string | null = null

  // Playwright does not let a beforeAll { request } be reused inside a test,
  // so the counter session owns its own context (same as e2e/cash.spec.ts).
  test.beforeAll(async ({ playwright }) => {
    shared = await playwright.request.newContext()
    manager = api(shared, (await devToken(shared, USERS.manager)).token)
    // Dev has three tables and other specs share them: take a free one, or
    // free one that a previous run left in cleaning.
    const all = await tables(manager)
    const pick = all.find((t) => t.status === "empty") ?? all.find((t) => t.status === "cleaning" && !t.active_check_id)
    expect(pick, "dev şubesinde boş (veya temizlikte) bir masa olmalı").toBeTruthy()
    table = pick as PosTable
    if (table.status !== "empty") await releaseTable(manager, table.id)
  })

  test.afterAll(async () => {
    if (checkId) await manager.post(`/api/v1/pos/checks/${checkId}/cancel`, { reason: "e2e temizlik" })
    await releaseTable(manager, table.id)
    await shared.dispose()
  })

  test("masa → seçenekli + seçeneksiz ürün → mutfağa gönder → adisyonda kalemler", async ({ page }) => {
    await loginAs(page, USERS.waiter)
    await page.setViewportSize(PHONE)

    // The waiter lands on the order screen's table plan (one floor plan).
    await tapFreeTable(page, table.name)
    await page.waitForURL((url) => url.pathname === "/pos/order" && url.searchParams.get("table") === table.id)
    await expect(page.getByRole("heading", { level: 1, name: table.name })).toBeVisible()

    // Payment/cancel/close never appear for a waiter on this screen.
    for (const hidden of [/ödeme/i, /iptal/i, /kapat$/i, /tahsil/i]) {
      await expect(page.getByRole("button", { name: hidden })).toHaveCount(0)
    }

    // Option product: panel opens with the required group pre-selected.
    await tapTile(page, PRODUCTS.adana)
    const panel = page.getByRole("dialog", { name: PRODUCTS.adana.name })
    await expect(panel.getByRole("radio", { name: /Az pişmiş/ })).toHaveAttribute("aria-checked", "true")
    await panel.getByRole("button", { name: /Ekstra sos/ }).click()
    await expect(panel.getByTestId("option-line-total")).toContainText("335,00")
    await panel.getByRole("button", { name: "Sepete ekle" }).click()
    await expect(panel).toBeHidden()

    // Plain product: one tap.
    await tapTile(page, PRODUCTS.ayran)
    await expect(page.getByTestId("cart-count")).toHaveText("2 kalem")
    await expect(page.getByTestId("cart-total")).toContainText("375,00")

    // The cart sheet lists both lines.
    await page.getByRole("button", { name: "Sepeti aç" }).click()
    const cart = page.getByRole("dialog")
    await expect(cart.getByTestId("cart-line")).toHaveCount(2)
    await cart.getByRole("button", { name: /Mutfağa gönder/ }).click()

    const done = page.getByRole("dialog", { name: "Mutfağa gönderildi" })
    await expect(done).toBeVisible()
    await expect(done).toContainText("2 kalem")
    await done.getByRole("button", { name: "Bu masaya ekle" }).click()
    await expect(page.getByTestId("cart-count")).toHaveText("0 kalem")

    const opened = (await tables(manager)).find((t) => t.id === table.id)
    checkId = opened?.active_check_id ?? null
    expect(checkId, "ilk gönderimde adisyon açılmış olmalı").toBeTruthy()

    const res = await manager.get(`/api/v1/pos/checks/${checkId}/orders`)
    const orders = (await res.json()) as Order[]
    const items = orders.flatMap((o) => o.items)
    expect(items).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          product_id: PRODUCTS.adana.id,
          quantity: 1,
          unit_price_amount: PRODUCTS.adana.price + 1_500,
          modifier_ids: expect.arrayContaining([AZ_PISMIS, EKSTRA_SOS]),
        }),
        expect.objectContaining({ product_id: PRODUCTS.ayran.id, quantity: 1, unit_price_amount: PRODUCTS.ayran.price }),
      ]),
    )

    // What the table already has is one tap away, and leads to the adisyon.
    const sent = page.getByTestId("sent-items")
    await sent.getByRole("button", { name: /Masada olanlar/ }).click()
    await expect(sent).toContainText(PRODUCTS.adana.name)
    await expect(sent).toContainText(PRODUCTS.ayran.name)
    await sent.getByRole("link", { name: "Adisyon detayı" }).click()
    await page.waitForURL((url) => url.pathname === `/pos/checks/${checkId}`)
    await expect(page.getByText(PRODUCTS.adana.name).first()).toBeVisible()
    await expect(page.getByText(PRODUCTS.ayran.name).first()).toBeVisible()
  })
})
