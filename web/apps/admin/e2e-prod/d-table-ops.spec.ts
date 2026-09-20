// (d) Masa taşıma / adisyon birleştirme / kalem taşıma — İzmit'te.

import { type APIRequestContext, expect, test } from "@playwright/test"

import {
  ACCOUNTS,
  type Check,
  type Order,
  type Principal,
  type Product,
  RUN,
  TAG,
  cleanupCheck,
  expectStatus,
  allTables,
  json,
  line,
  openCheck,
  placeOrderOk,
  principal,
  productByName,
  releaseTable,
} from "./fixtures/prod"

test.describe.configure({ mode: "serial" })

test.describe("(d) masa ve adisyon işlemleri (İzmit)", () => {
  let cashier: Principal
  let kitchen: Principal
  let manager: Principal
  let shared: APIRequestContext
  let branchId: string
  let product: Product
  const opened: string[] = []
  const touchedTables: string[] = []

  // Playwright, beforeAll'da alınan { request } fixture'ının testlerde
  // yeniden kullanılmasına izin vermez — paylaşılan oturum kendi
  // APIRequestContext'ini açar ve afterAll'da kapatır (e2e/cash.spec.ts ile
  // aynı desen).
  test.beforeAll(async ({ playwright }) => {
    shared = await playwright.request.newContext()
    cashier = await principal(shared, ACCOUNTS.cashierIzmit())
    kitchen = await principal(shared, ACCOUNTS.kitchenIzmit())
    manager = await principal(shared, ACCOUNTS.manager())
    branchId = cashier.ctx.branch_id as string
    product = await productByName(cashier.api, "Cheese Burger", branchId)
  })

  test.afterAll(async () => {
    for (const id of opened) await cleanupCheck(cashier.api, kitchen.api, id)
    for (const id of new Set(touchedTables)) await releaseTable(manager.api, branchId, id)
    await shared.dispose()
  })

  test("adisyon boş masaya taşınır; bilinmeyen masa reddedilir", async () => {
    const free = (await allTables(cashier.api, branchId)).filter((t) => t.status === "empty")
    expect(free.length, "İzmit'te en az iki boş masa olmalı").toBeGreaterThanOrEqual(2)

    const check = await openCheck(cashier.api, branchId, `${RUN}-d-transfer`, free[0].id)
    opened.push(check.id)
    touchedTables.push(free[0].id, free[1].id)
    await placeOrderOk(cashier.api, branchId, check.id, [line(product)])

    const moved = await json<Check>(
      await cashier.api.postNew(`/api/v1/pos/checks/${check.id}/transfer`, { table_id: free[1].id }),
      200,
    )
    expect(moved.table_id).toBe(free[1].id)

    const unknown = await cashier.api.postNew(`/api/v1/pos/checks/${check.id}/transfer`, {
      table_id: "00000000-0000-4000-8000-000000000000",
    })
    expect(unknown.status()).toBeGreaterThanOrEqual(400)
  })

  test("adisyonlar birleşir: kaynak 'merged' olur, tutar hedefe geçer", async () => {
    const free = (await allTables(cashier.api, branchId)).filter((t) => t.status === "empty")
    expect(free.length).toBeGreaterThanOrEqual(2)

    const target = await openCheck(cashier.api, branchId, `${RUN}-d-merge-hedef`, free[0].id)
    const source = await openCheck(cashier.api, branchId, `${RUN}-d-merge-kaynak`, free[1].id)
    opened.push(target.id, source.id)
    touchedTables.push(free[0].id, free[1].id)

    await placeOrderOk(cashier.api, branchId, target.id, [line(product)])
    await placeOrderOk(cashier.api, branchId, source.id, [line(product)])

    await expectStatus(await cashier.api.postNew(`/api/v1/pos/checks/${target.id}/merge`, { source_check_id: source.id }), 200)

    const merged = await json<Check>(await cashier.api.get(`/api/v1/pos/checks/${source.id}`), 200)
    expect(merged.status, "birleşen adisyon iptal değil 'merged' olmalı").toBe("merged")
    expect(merged.merged_into_check_id).toBe(target.id)
    expect(merged.total).toBe(0)

    const into = await json<Check>(await cashier.api.get(`/api/v1/pos/checks/${target.id}`), 200)
    expect(into.total).toBe(product.price_amount * 2)
  })

  test("kalem taşıma: seçilen sipariş hedef adisyona geçer, yabancı kalem reddedilir", async () => {
    const free = (await allTables(cashier.api, branchId)).filter((t) => t.status === "empty")
    expect(free.length).toBeGreaterThanOrEqual(2)

    const source = await openCheck(cashier.api, branchId, `${RUN}-d-move-kaynak`, free[0].id)
    const target = await openCheck(cashier.api, branchId, `${RUN}-d-move-hedef`, free[1].id)
    opened.push(source.id, target.id)
    touchedTables.push(free[0].id, free[1].id)

    const order = await placeOrderOk(cashier.api, branchId, source.id, [line(product, 2)])
    const itemId = order.items[0].id

    await expectStatus(
      await cashier.api.postNew(`/api/v1/pos/checks/${source.id}/move-items`, {
        target_check_id: target.id,
        order_item_ids: [itemId],
      }),
      200,
    )

    const targetOrders = await json<Order[]>(await cashier.api.get(`/api/v1/pos/checks/${target.id}/orders`), 200)
    expect(targetOrders.flatMap((o) => o.items).map((i) => i.id)).toContain(itemId)

    const foreign = await cashier.api.postNew(`/api/v1/pos/checks/${source.id}/move-items`, {
      target_check_id: target.id,
      order_item_ids: ["00000000-0000-4000-8000-000000000000"],
    })
    expect(foreign.status()).toBeGreaterThanOrEqual(400)
  })

  test(`${TAG}: garson adisyon açıp sipariş alır; kapatma, iptal, ret ve ödeme yetkisi yok`, async () => {
    const waiter = await principal(shared, ACCOUNTS.waiterIzmit())
    // Garson sipariş alır: pos.table.read + check açma/okuma + sipariş
    // verme/okuma + katalog okuma (identity/000019, authz.rego pos_waiter_actions).
    await expectStatus(await waiter.api.get(`/api/v1/pos/tables?branch_id=${branchId}`), 200)

    const free = (await allTables(cashier.api, branchId)).find((t) => t.status === "empty")
    expect(free, "İzmit'te en az bir boş masa olmalı").toBeTruthy()
    touchedTables.push((free as { id: string }).id)

    const check = await openCheck(waiter.api, branchId, `${RUN}-d-garson`, (free as { id: string }).id)
    opened.push(check.id)
    const order = await placeOrderOk(waiter.api, branchId, check.id, [line(product)])
    await expectStatus(await waiter.api.get(`/api/v1/pos/orders/${order.id}`), 200)
    await expectStatus(await waiter.api.get(`/api/v1/pos/checks/${check.id}`), 200)

    // Sınır: kabul/ret/iptal kasiyerde, kapatma ve ödeme para hareketi.
    await expectStatus(await waiter.api.postNew(`/api/v1/pos/orders/${order.id}/accept`, {}), 403)
    await expectStatus(await waiter.api.postNew(`/api/v1/pos/orders/${order.id}/reject`, { reason: "garson" }), 403)
    await expectStatus(await waiter.api.postNew(`/api/v1/pos/orders/${order.id}/cancel`, {}), 403)
    await expectStatus(await waiter.api.postNew(`/api/v1/pos/checks/${check.id}/close`, {}), 403)
    await expectStatus(await waiter.api.postNew(`/api/v1/pos/checks/${check.id}/cancel`, {}), 403)
    await expectStatus(await waiter.api.postNew("/api/v1/payments", {}), 403)
  })
})
