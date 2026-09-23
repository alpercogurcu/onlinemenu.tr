// (i) Garson web sipariş ekranı (/pos/order) — canlı test hesaplarıyla.
//
// Garson girişte masa planına (/pos/order) düşer, masaya dokunur, sipariş ekranında ürünü KENDİ
// şubesinin fiyatıyla görür (ADR-DATA-009), sepete ekler ve mutfağa gönderir.
// American Smash Burger: Serdivan tenant fiyatı 470 TL, İzmit override 490 TL
// (bkz. a-branch-pricing.spec.ts). Sunucu sipariş satırını katalogdan yeniden
// fiyatlar; ekran yanlış fiyat gösterseydi gönderim 422 price_mismatch alırdı.
// Temizlik kasa hesabıyla yapılır: garson adisyon iptal edemez, masa boşaltamaz.

import { type APIRequestContext, type Browser, type Page, expect, test } from "@playwright/test"

import {
  ACCOUNTS,
  type Account,
  type Order,
  type Principal,
  allTables,
  cleanupCheck,
  json,
  loginAdmin,
  principal,
  productByName,
  releaseTable,
  tablePlan,
} from "./fixtures/prod"

const SMASH = "American Smash Burger"

interface Case {
  branch: string
  waiter: () => Account
  cashier: () => Account
  kitchen: () => Account
  price: number
}

const CASES: Case[] = [
  {
    branch: "Serdivan",
    waiter: ACCOUNTS.waiterSerdivan,
    cashier: ACCOUNTS.cashierSerdivan,
    kitchen: ACCOUNTS.kitchenSerdivan,
    price: 47_000,
  },
  {
    branch: "İzmit",
    waiter: ACCOUNTS.waiterIzmit,
    cashier: ACCOUNTS.cashierIzmit,
    kitchen: ACCOUNTS.kitchenIzmit,
    price: 49_000,
  },
]

const lira = new Intl.NumberFormat("tr-TR", { style: "currency", currency: "TRY" })
const escapeRe = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")

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

/** Walks the category chips until the tile with this name AND price shows up. */
async function tapTile(page: Page, name: string, price: number) {
  const tile = page.getByRole("button", {
    name: new RegExp(`^${escapeRe(name)}, ${escapeRe(lira.format(price / 100))}`),
  })
  const tabs = page.getByRole("tablist", { name: "Kategoriler" }).getByRole("tab")
  await expect(tabs.first()).toBeVisible()
  for (let i = 0; (await tile.count()) === 0 && i < (await tabs.count()); i++) {
    await tabs.nth(i).click()
  }
  await expect(tile, `${name} ${lira.format(price / 100)} ile görünmeli`).toBeVisible()
  await tile.click()
}

test.describe.configure({ mode: "serial" })

test.describe("(i) garson web sipariş ekranı", () => {
  let shared: APIRequestContext
  let manager: Principal

  test.beforeAll(async ({ playwright }) => {
    shared = await playwright.request.newContext()
    manager = await principal(shared, ACCOUNTS.manager())
  })

  test.afterAll(async () => {
    await shared.dispose()
  })

  for (const c of CASES) {
    test(`${c.branch} garsonu burger'i ${c.price / 100} TL görür ve mutfağa gönderir`, async ({ browser }) => {
      const waiter = await principal(shared, c.waiter())
      const cashier = await principal(shared, c.cashier())
      const kitchen = await principal(shared, c.kitchen())
      const branchId = waiter.ctx.branch_id as string
      expect(branchId, "garsonun üyeliği şube kapsamlı olmalı (SEC-005)").toBeTruthy()

      const product = await productByName(waiter.api, SMASH, branchId)
      expect(product.price_amount).toBe(c.price)

      const free = (await allTables(manager.api, branchId)).find((t) => t.status === "empty")
      expect(free, `${c.branch}'te en az bir boş masa olmalı`).toBeTruthy()
      const table = free as { id: string; name: string }

      let checkId: string | null = null
      const page = await openAs(browser, c.waiter())
      try {
        // The waiter's home is the order screen's table plan.
        await page.waitForURL((url) => url.pathname === "/pos/order", { timeout: 30_000 })
        await tapFreeTable(page, table.name)
        await page.waitForURL((url) => url.searchParams.get("table") === table.id)

        await tapTile(page, SMASH, c.price)
        // Whatever option groups the burger has, the required ones come
        // pre-selected: "Sepete ekle" is the second tap.
        const panel = page.getByRole("dialog", { name: SMASH })
        const hasOptions = await panel
          .waitFor({ state: "visible", timeout: 3_000 })
          .then(() => true)
          .catch(() => false)
        if (hasOptions) {
          await panel.getByRole("button", { name: "Sepete ekle" }).click()
          await expect(panel).toBeHidden()
        }

        await page.getByRole("button", { name: /Mutfağa gönder/ }).filter({ visible: true }).first().click()
        await expect(page.getByRole("dialog", { name: "Mutfağa gönderildi" })).toBeVisible()

        checkId = await activeCheckOf(manager, branchId, table.id)
        expect(checkId, "ilk gönderimde adisyon açılmış olmalı").toBeTruthy()

        const orders = await json<Order[]>(await manager.api.get(`/api/v1/pos/checks/${checkId}/orders`), 200)
        const burger = orders.flatMap((o) => o.items).find((i) => i.product_name === SMASH)
        expect(burger, "burger adisyonda olmalı").toBeTruthy()
        // The order response carries no base price; with no options the unit
        // price IS the branch price. With options, the server's acceptance
        // (no 422 price_mismatch) is the proof the base was right.
        if (hasOptions) expect(burger?.unit_price_amount).toBeGreaterThanOrEqual(c.price)
        else expect(burger?.unit_price_amount).toBe(c.price)
      } finally {
        await page.context().close()
        // A send that failed half-way may still have opened the check.
        checkId ??= await activeCheckOf(manager, branchId, table.id).catch(() => null)
        if (checkId) await cleanupCheck(cashier.api, kitchen.api, checkId)
        await releaseTable(manager.api, branchId, table.id)
      }
    })
  }
})

async function activeCheckOf(manager: Principal, branchId: string, tableId: string): Promise<string | null> {
  const plan = await tablePlan(manager.api, branchId)
  const table = plan
    .flatMap((z) => z.tables as { id: string; active_check_id?: string | null }[])
    .find((t) => t.id === tableId)
  return table?.active_check_id ?? null
}

async function openAs(browser: Browser, acct: Account): Promise<Page> {
  // Separate context per role: a shared Keycloak SSO cookie would log the
  // second waiter in as the first.
  const context = await browser.newContext()
  const page = await context.newPage()
  await loginAdmin(page, acct)
  return page
}
