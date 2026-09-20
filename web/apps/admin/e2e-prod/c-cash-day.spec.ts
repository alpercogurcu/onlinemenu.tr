// (c) Serdivan'da tam kasa günü — canlı prod akışının uçtan uca provası:
// kasa aç → adisyon → seçeneksiz + seçenekli sipariş → mutfak (KDS arayüzü,
// mutfak hesabı) kabul/ilerlet → kalem bazlı + kalan nakit ödeme (mock ÖKC
// fişi) → adisyon kapat → masayı kasiyer temizler → kasa sayımı (fark) →
// kasa kapat → şube bazlı gün sonu raporu.
//
// Seçenek verisi: prod kataloğunda hiç modifier yok (b2b'de de yoktu). Test
// kendi `PRODTEST Ekstra` grubunu açar, kullanır ve sonunda üründen çözer.

import { type APIRequestContext, expect, test } from "@playwright/test"

import {
  ACCOUNTS,
  type Api,
  type CashSession,
  type Check,
  type Order,
  type Principal,
  type Product,
  RUN,
  TAG,
  activeSession,
  allTables,
  cashSale,
  expectStatus,
  gotoSpa,
  json,
  line,
  loginAdmin,
  openCheck,
  payCash,
  placeOrderOk,
  principal,
  productByName,
  saleDetails,
  serveLiveOrders,
  waitCompleted,
} from "./fixtures/prod"

const OPENING = 50_000
const SHORTFALL = 2_500
const EXTRA_DELTA = 2_000

test.describe.configure({ mode: "serial" })

test.describe("(c) Serdivan kasa günü", () => {
  let manager: Principal
  let cashier: Principal
  let kitchen: Principal
  let shared: APIRequestContext
  let branchId: string
  let burger: Product
  let groupId: string
  let modifierId: string
  let tableId: string
  let tableLabel: string
  let sessionId: string
  let checkId: string
  const windowFrom = new Date(Date.now() - 5 * 60_000).toISOString()
  const windowTo = new Date(Date.now() + 6 * 3_600_000).toISOString()

  // beforeAll'daki { request } testlerde yeniden kullanılamaz (Playwright);
  // üç oturum ortak bir APIRequestContext üzerinde yaşar.
  test.beforeAll(async ({ playwright }) => {
    shared = await playwright.request.newContext()
    manager = await principal(shared, ACCOUNTS.manager())
    cashier = await principal(shared, ACCOUNTS.cashierSerdivan())
    kitchen = await principal(shared, ACCOUNTS.kitchenSerdivan())
    branchId = cashier.ctx.branch_id as string
    burger = await productByName(cashier.api, "Cheese Burger", branchId)
  })

  test.afterAll(async () => {
    // Test verisi bırakma: seçenek grubunu üründen çöz ve sil.
    if (groupId) {
      await manager.api.del(`/api/v1/catalog/products/${burger.id}/modifier-groups/${groupId}`).catch(() => undefined)
      // Grup silinemeyebilir (geçmiş sipariş satırları ona bakıyor olabilir);
      // o durumda PRODTEST etiketiyle kataloğda kalır ve raporda belirtilir.
      await manager.api.del(`/api/v1/catalog/modifier-groups/${groupId}`).catch(() => undefined)
    }
    await shared.dispose()
  })

  test("kasa açılır; ikinci açış 409, negatif açılış 422", async () => {
    const leftover = await activeSession(manager.api, branchId)
    if (leftover) {
      await manager.api.post(`/api/v1/payments/cash-sessions/${leftover.id}/closing-count`, {
        closing_counted_amount: leftover.expected_close,
        notes: `${TAG}: önceki koşudan kalan`,
      })
      await manager.api.post(`/api/v1/payments/cash-sessions/${leftover.id}/close`)
    }
    expect(await activeSession(manager.api, branchId)).toBeNull()

    const opened = await json<CashSession>(
      await cashier.api.post("/api/v1/payments/cash-sessions", {
        branch_id: branchId,
        opening_counted_amount: OPENING,
        opening_notes: RUN,
      }),
      201,
    )
    sessionId = opened.id
    expect(opened).toMatchObject({ status: "opened", opening_counted_amount: OPENING, expected_close: OPENING })

    await expectStatus(
      await manager.api.post("/api/v1/payments/cash-sessions", {
        branch_id: branchId,
        opening_counted_amount: OPENING,
      }),
      409,
    )
    await expectStatus(
      await cashier.api.post("/api/v1/payments/cash-sessions", { branch_id: branchId, opening_counted_amount: -1 }),
      422,
    )
  })

  test("masaya adisyon açılır; seçeneksiz ve seçenekli sipariş girilir", async () => {
    const tables = await allTables(cashier.api, branchId)
    const free = tables.find((t) => t.status === "empty")
    expect(free, "Serdivan'da boş masa olmalı").toBeTruthy()
    tableId = (free as { id: string }).id
    tableLabel = (free as { name: string }).name

    const check = await openCheck(cashier.api, branchId, `${RUN}-c`, tableId)
    checkId = check.id
    expect(check.table_id).toBe(tableId)

    // Seçeneksiz sipariş.
    await placeOrderOk(cashier.api, branchId, checkId, [line(burger)])

    // Seçenek grubu (prod kataloğunda modifier yok — test kendi grubunu açar).
    const group = await json<{ id: string }>(
      await manager.api.post("/api/v1/catalog/modifier-groups", {
        name: `${TAG} Ekstra`,
        selection_type: "multiple",
        min_selections: 0,
        is_required: false,
      }),
      201,
    )
    groupId = group.id
    const modifier = await json<{ id: string }>(
      await manager.api.post(`/api/v1/catalog/modifier-groups/${groupId}/modifiers`, {
        name: `${TAG} Ekstra Peynir`,
        price_delta: EXTRA_DELTA,
        is_active: true,
      }),
      201,
    )
    modifierId = modifier.id
    await expectStatus(
      await manager.api.post(`/api/v1/catalog/products/${burger.id}/modifier-groups`, { group_id: groupId }),
      204,
    )

    // Seçenekli satır: fiyat taban + price_delta olmalı, aksi hâlde 422.
    const withOption = await placeOrderOk(cashier.api, branchId, checkId, [
      line(burger, 1, burger.price_amount + EXTRA_DELTA, [modifierId]),
    ])
    expect(withOption.items[0].modifier_ids).toEqual([modifierId])

    const check2 = await json<Check>(await cashier.api.get(`/api/v1/pos/checks/${checkId}`), 200)
    expect(check2.total).toBe(burger.price_amount * 2 + EXTRA_DELTA)
  })

  test("mutfak ekranında (KDS arayüzü) bilet ilerletilir", async ({ page }) => {
    // Kabul kasa kararıdır (pos.order.accept): mutfak ekranında "Kabul Et"
    // düğmesi yoktur, kasiyer API'den kabul eder. Her iki sipariş de kabul
    // edilir, aksi hâlde aynı masanın panoda hem bekleyen hem kabul edilmiş
    // kartı olur ve seçim belirsizleşir.
    const pending = (await json<Order[]>(await cashier.api.get(`/api/v1/pos/checks/${checkId}/orders`), 200)).filter(
      (o) => o.status === "pending",
    )
    expect(pending.length).toBeGreaterThan(0)
    for (const order of pending) {
      await expectStatus(await cashier.api.post(`/api/v1/pos/orders/${order.id}/accept`), 200)
    }

    await loginAdmin(page, ACCOUNTS.kitchenSerdivan())
    await gotoSpa(page, "/pos/kitchen")
    await expect(page.getByText("Canlı", { exact: true })).toBeVisible()

    // Kart, adisyonun masasıyla başlıklanır (kitchen/page.tsx: order.tableLabel).
    // Durum ilerleyince kart başka sütuna YENİDEN BAĞLANIR, bu yüzden her adım
    // eski düğüme tutunmak yerine kendi düğmesiyle yeniden bulunur.
    const cardWith = (button: string) =>
      page
        .locator("[data-kds-root] [data-slot=card]")
        .filter({ hasText: tableLabel })
        .filter({ has: page.getByRole("button", { name: button, exact: true }) })
        .first()

    const startCard = cardWith("Hazırlamaya Başla")
    await expect(startCard).toBeVisible()
    await startCard.getByRole("button", { name: "Hazırlamaya Başla", exact: true }).click()

    const readyCard = cardWith("Hazır")
    await expect(readyCard).toBeVisible()
    await readyCard.getByRole("button", { name: "Hazır", exact: true }).click()

    await expect
      .poll(
        async () =>
          (await json<Order[]>(await kitchen.api.get(`/api/v1/pos/checks/${checkId}/orders`), 200)).some(
            (o) => o.status === "ready",
          ),
        { timeout: 45_000 },
      )
      .toBe(true)
  })

  test("kalem bazlı + kalan nakit ödeme; mock ÖKC fişi kesilir", async () => {
    const check = await json<Check>(await cashier.api.get(`/api/v1/pos/checks/${checkId}`), 200)
    const partial = Math.round(check.total / 3)
    const rest = check.total - partial

    // Idempotency-Key zorunlu (ADR-SEC-003): anahtarsız istek 422.
    await expectStatus(
      await cashier.api.post("/api/v1/payments", cashSale(branchId, checkId, "PRODTEST kısmi", partial)),
      422,
    )

    const first = await payCash(cashier.api, branchId, checkId, "PRODTEST kalem", partial, `${RUN}-pay-1`)
    // Aynı anahtar = aynı ödeme, ikinci tahsilat yok.
    const replay = await cashier.api.post("/api/v1/payments", cashSale(branchId, checkId, "PRODTEST kalem", partial), `${RUN}-pay-1`)
    expect((await json<{ id: string }>(replay, 201)).id).toBe(first.id)
    await waitCompleted(manager.api, first.id)

    // Eksik ödemeyle kapanmaz.
    const early = await cashier.api.postNew(`/api/v1/pos/checks/${checkId}/close`)
    await expectStatus(early, 409)
    expect(await early.json()).toMatchObject({ code: "insufficient_payment" })

    const second = await payCash(cashier.api, branchId, checkId, "PRODTEST kalan", rest, `${RUN}-pay-2`)
    const completed = await waitCompleted(manager.api, second.id)
    expect(completed.fiscal_receipt_id, "mock ÖKC fiş numarası üretmeli").not.toBeNull()
  })

  test("siparişler servis edilir, adisyon kapanır, masayı kasiyer temizler", async () => {
    // Kapatmadan ÖNCE: kapalı adisyonun siparişleri API'den ilerletilemez.
    await serveLiveOrders(cashier.api, kitchen.api, checkId)

    const closed = await json<Check>(await cashier.api.postNew(`/api/v1/pos/checks/${checkId}/close`), 200)
    expect(closed.status).toBe("closed")

    // Kapalı adisyon ne sipariş ne para alır.
    const lateOrder = await cashier.api.postNew("/api/v1/pos/orders", {
      branch_id: branchId,
      check_id: checkId,
      order_channel: "dine_in",
      items: [line(burger)],
    })
    await expectStatus(lateOrder, 409)
    expect(await lateOrder.json()).toMatchObject({ code: "check_not_open" })

    // Masa cleaning'e düşer; kasiyer empty'ye çekebilir (pos.table.clean).
    const tables = await allTables(cashier.api, branchId)
    const table = tables.find((t) => t.id === tableId)
    expect(table?.status).toBe("cleaning")
    await expectStatus(await cashier.api.post(`/api/v1/pos/tables/${tableId}/status`, { status: "empty" }), 200)
    const after = (await allTables(cashier.api, branchId)).find((t) => t.id === tableId)
    expect(after?.status).toBe("empty")
  })

  test("kasa sayımı farkı raporlar, kasa kapanır", async () => {
    const before = await activeSession(cashier.api, branchId)
    expect(before?.cash_payments_taken).toBeGreaterThan(0)

    await json(
      await cashier.api.postNew(`/api/v1/payments/cash-sessions/${sessionId}/movements`, {
        direction: "out",
        amount_minor: 5_000,
        reason: `${TAG}: tedarikçi avansı`,
      }),
      201,
    )

    const session = await activeSession(cashier.api, branchId)
    expect(session?.movements_net).toBe(-5_000)

    const counted = (session as CashSession).expected_close - SHORTFALL
    const counting = await json<CashSession>(
      await cashier.api.post(`/api/v1/payments/cash-sessions/${sessionId}/closing-count`, {
        closing_counted_amount: counted,
        notes: `${TAG}: kasa açığı denemesi`,
      }),
      200,
    )
    expect(counting.difference).toBe(-SHORTFALL)

    const closed = await json<CashSession>(await manager.api.post(`/api/v1/payments/cash-sessions/${sessionId}/close`), 200)
    expect(closed.status).toBe("closed")
    expect(closed.difference).toBe(-SHORTFALL)
    expect(await activeSession(manager.api, branchId)).toBeNull()
  })

  test("gün sonu raporu şube bazlıdır ve bu günü içerir", async () => {
    const report = await saleDetails(manager.api, branchId, windowFrom, windowTo)
    expect(report.sales.closed_check_count).toBeGreaterThanOrEqual(1)
    expect(report.sales.gross).toBeGreaterThanOrEqual(burger.price_amount * 2 + EXTRA_DELTA)

    const cash = report.payments.find((p) => p.method === "cash" && p.status === "completed")
    expect(cash?.count).toBeGreaterThanOrEqual(2)

    const row = report.cash_sessions.find((s) => s.id === sessionId)
    expect(row?.status).toBe("closed")
    expect(row?.difference).toBe(-SHORTFALL)

    // Rapor şube kapsamlıdır: İzmit raporunda Serdivan'ın kasa oturumu yoktur.
    const izmit = await principal(shared, ACCOUNTS.cashierIzmit())
    const izmitReport = await saleDetails(manager.api, izmit.ctx.branch_id as string, windowFrom, windowTo)
    expect(izmitReport.cash_sessions.map((s) => s.id)).not.toContain(sessionId)
  })
})
