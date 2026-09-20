import { randomUUID } from "node:crypto"

import { type APIRequestContext, expect, test } from "@playwright/test"

import { API_URL, BRANCH_ID, PRODUCT_ID, USERS, devToken } from "./fixtures/auth"

const POS = `${API_URL}/api/v1/pos`
const UNIT_PRICE = 32000

type Headers = { Authorization: string }

type Check = {
  id: string
  status: string
  total?: number
  table_id: string | null
  table_label: string
  merged_into_check_id?: string
}
type OrderItem = { id: string; product_name: string; unit_price_amount: number }
type Order = { id: string; status: string; check_id: string | null; items: OrderItem[] }
type PosTable = { id: string; name: string; status: string }

async function headersFor(request: APIRequestContext, email: string): Promise<Headers> {
  const { token } = await devToken(request, email)
  return { Authorization: `Bearer ${token}` }
}

function item(quantity: number) {
  return {
    product_id: PRODUCT_ID,
    product_name: "Adana Kebap",
    product_price_amount: UNIT_PRICE,
    product_currency: "TRY",
    tax_rate_bps: 1000,
    quantity,
    unit_price_amount: UNIT_PRICE,
  }
}

async function openCheck(request: APIRequestContext, headers: Headers, label: string): Promise<string> {
  const res = await request.post(`${POS}/checks`, {
    headers,
    data: { branch_id: BRANCH_ID, table_label: label, pax: 2 },
  })
  expect(res.status(), await res.text()).toBe(201)
  return ((await res.json()) as Check).id
}

async function placeOrderFull(
  request: APIRequestContext,
  headers: Headers,
  checkId: string,
  quantities: number[],
): Promise<Order> {
  const res = await request.post(`${POS}/orders`, {
    headers: { ...headers, "Idempotency-Key": `e2e-orders-${randomUUID()}` },
    data: {
      branch_id: BRANCH_ID,
      check_id: checkId,
      order_channel: "dine_in",
      items: quantities.map(item),
    },
  })
  expect(res.status(), await res.text()).toBe(201)
  return (await res.json()) as Order
}

async function placeOrder(
  request: APIRequestContext,
  headers: Headers,
  checkId: string,
  quantities: number[],
): Promise<string> {
  return (await placeOrderFull(request, headers, checkId, quantities)).id
}

async function getCheck(request: APIRequestContext, headers: Headers, checkId: string): Promise<Check> {
  const res = await request.get(`${POS}/checks/${checkId}`, { headers })
  expect(res.status(), await res.text()).toBe(200)
  return (await res.json()) as Check
}

async function getOrder(request: APIRequestContext, headers: Headers, orderId: string): Promise<Order> {
  const res = await request.get(`${POS}/orders/${orderId}`, { headers })
  expect(res.status(), await res.text()).toBe(200)
  return (await res.json()) as Order
}

async function listCheckOrders(request: APIRequestContext, headers: Headers, checkId: string): Promise<Order[]> {
  const res = await request.get(`${POS}/checks/${checkId}/orders`, { headers })
  expect(res.status(), await res.text()).toBe(200)
  return (await res.json()) as Order[]
}

function advance(request: APIRequestContext, headers: Headers, orderId: string, status: string) {
  return request.post(`${POS}/orders/${orderId}/advance`, { headers, data: { status } })
}

function cancelOrder(request: APIRequestContext, headers: Headers, orderId: string) {
  return request.post(`${POS}/orders/${orderId}/cancel`, { headers })
}

async function accept(request: APIRequestContext, headers: Headers, orderId: string) {
  const res = await request.post(`${POS}/orders/${orderId}/accept`, { headers })
  expect(res.status(), await res.text()).toBe(200)
}

// Cancelling a check cancels its live orders too, so this alone keeps the
// shared kitchen board clean. Best-effort: the assertions in the test body
// are what fail.
async function cleanup(request: APIRequestContext, headers: Headers, checkId: string) {
  await request.post(`${POS}/checks/${checkId}/cancel`, { headers, data: {} })
}

test.describe("sipariş yaşam döngüsü ve adisyon toplamı", () => {
  test("yanlış sipariş reddedilince adisyon toplamı düşer, doğru sipariş toplamı yeniden kurar", async ({
    request,
  }) => {
    const label = `E2E-RED-${Date.now().toString(36)}`
    const headers = await headersFor(request, USERS.manager)
    const checkId = await openCheck(request, headers, label)

    try {
      const wrongOrderId = await placeOrder(request, headers, checkId, [1, 2])
      expect((await getCheck(request, headers, checkId)).total).toBe(3 * UNIT_PRICE)

      const reject = await request.post(`${POS}/orders/${wrongOrderId}/reject`, {
        headers,
        data: { reason: "Garson yanlış masaya girdi" },
      })
      expect(reject.status(), await reject.text()).toBe(200)
      const rejected = (await reject.json()) as Order & { rejection_reason: string; items: unknown[] }
      expect(rejected.status).toBe("rejected")
      expect(rejected.rejection_reason).toBe("Garson yanlış masaya girdi")
      expect(rejected.items).toHaveLength(2)

      // The rejected ticket stays on the check for the audit trail, but its
      // lines no longer count towards the bill.
      const orders = await listCheckOrders(request, headers, checkId)
      expect(orders.map((o) => [o.id, o.status])).toEqual([[wrongOrderId, "rejected"]])
      expect((await getCheck(request, headers, checkId)).total).toBe(0)

      const rightOrderId = await placeOrder(request, headers, checkId, [1])
      expect((await getCheck(request, headers, checkId)).total).toBe(UNIT_PRICE)

      const list = await request.get(`${POS}/checks?status=open&branch_id=${BRANCH_ID}`, { headers })
      expect(list.status()).toBe(200)
      const listed = ((await list.json()) as Check[]).find((c) => c.id === checkId)
      expect(listed?.total).toBe(UNIT_PRICE)

      const statuses = Object.fromEntries((await listCheckOrders(request, headers, checkId)).map((o) => [o.id, o.status]))
      expect(statuses).toEqual({ [wrongOrderId]: "rejected", [rightOrderId]: "pending" })

      const again = await request.post(`${POS}/orders/${wrongOrderId}/reject`, { headers, data: { reason: "tekrar" } })
      expect(again.status()).toBe(409)
      expect(((await again.json()) as { code: string }).code).toBe("invalid_transition")
    } finally {
      await cleanup(request, headers, checkId)
    }
  })

  test("kabul edilen sipariş reddedilemez ama iptal edilir; teslim edilen iptal edilemez", async ({ request }) => {
    const label = `E2E-IPT-${Date.now().toString(36)}`
    const headers = await headersFor(request, USERS.manager)
    const checkId = await openCheck(request, headers, label)

    try {
      const cancelledId = await placeOrder(request, headers, checkId, [1])
      const deliveredId = await placeOrder(request, headers, checkId, [2])
      expect((await getCheck(request, headers, checkId)).total).toBe(3 * UNIT_PRICE)

      await accept(request, headers, cancelledId)
      const reject = await request.post(`${POS}/orders/${cancelledId}/reject`, { headers, data: { reason: "geç kaldı" } })
      expect(reject.status()).toBe(409)
      expect(((await reject.json()) as { code: string }).code).toBe("invalid_transition")
      expect((await getOrder(request, headers, cancelledId)).status).toBe("accepted")

      // Cancellation has its own endpoint; /advance refuses it so kitchen
      // roles (who hold only pos.order.advance) cannot cancel.
      const viaAdvance = await advance(request, headers, cancelledId, "cancelled")
      expect(viaAdvance.status()).toBe(422)
      expect(((await viaAdvance.json()) as { code: string }).code).toBe("use_dedicated_endpoint")
      expect((await getOrder(request, headers, cancelledId)).status).toBe("accepted")

      const cancel = await cancelOrder(request, headers, cancelledId)
      expect(cancel.status(), await cancel.text()).toBe(200)
      expect(((await cancel.json()) as Order).status).toBe("cancelled")
      expect((await getCheck(request, headers, checkId)).total).toBe(2 * UNIT_PRICE)

      await accept(request, headers, deliveredId)
      for (const status of ["preparing", "ready", "delivered"]) {
        const res = await advance(request, headers, deliveredId, status)
        expect(res.status(), `${status}: ${await res.text()}`).toBe(200)
      }
      const lateCancel = await cancelOrder(request, headers, deliveredId)
      expect(lateCancel.status()).toBe(409)
      expect(((await lateCancel.json()) as { code: string }).code).toBe("invalid_transition")
      expect((await getOrder(request, headers, deliveredId)).status).toBe("delivered")

      // Delivered food is served and must still be billed.
      expect((await getCheck(request, headers, checkId)).total).toBe(2 * UNIT_PRICE)
    } finally {
      await cleanup(request, headers, checkId)
    }
  })

  test("tüm siparişleri reddedilmiş sıfır tutarlı adisyon ödemesiz kapanır", async ({ request }) => {
    const label = `E2E-SIF-${Date.now().toString(36)}`
    const headers = await headersFor(request, USERS.manager)
    const checkId = await openCheck(request, headers, label)

    try {
      const orderId = await placeOrder(request, headers, checkId, [1])
      const reject = await request.post(`${POS}/orders/${orderId}/reject`, { headers, data: { reason: "yanlış sipariş" } })
      expect(reject.status(), await reject.text()).toBe(200)
      expect((await getCheck(request, headers, checkId)).total).toBe(0)

      // Nothing is owed, so the payment guard (paid >= total) is satisfied
      // without any payment row and the close goes through.
      const close = await request.post(`${POS}/checks/${checkId}/close`, {
        headers: { ...headers, "Idempotency-Key": `e2e-orders-${randomUUID()}` },
      })
      expect(close.status(), await close.text()).toBe(200)
      expect(((await close.json()) as Check).status).toBe("closed")

      const late = await request.post(`${POS}/orders`, {
        headers: { ...headers, "Idempotency-Key": `e2e-orders-${randomUUID()}` },
        data: { branch_id: BRANCH_ID, check_id: checkId, order_channel: "dine_in", items: [item(1)] },
      })
      expect(late.status()).toBe(409)
      expect(((await late.json()) as { code: string }).code).toBe("check_not_open")
    } finally {
      await cleanup(request, headers, checkId)
    }
  })

  test("adisyon iptali içindeki canlı siparişleri de iptal eder", async ({ request }) => {
    const label = `E2E-HAY-${Date.now().toString(36)}`
    const headers = await headersFor(request, USERS.manager)
    const checkId = await openCheck(request, headers, label)

    try {
      const pendingId = await placeOrder(request, headers, checkId, [1])
      const acceptedId = await placeOrder(request, headers, checkId, [1])
      await accept(request, headers, acceptedId)

      const cancel = await request.post(`${POS}/checks/${checkId}/cancel`, { headers, data: { reason: "e2e" } })
      expect(cancel.status(), await cancel.text()).toBe(200)
      expect(((await cancel.json()) as Check).status).toBe("cancelled")

      // The kitchen must not keep tickets of a check that no longer exists.
      expect((await getOrder(request, headers, pendingId)).status).toBe("cancelled")
      expect((await getOrder(request, headers, acceptedId)).status).toBe("cancelled")

      for (const res of [
        await request.post(`${POS}/orders/${pendingId}/accept`, { headers }),
        await advance(request, headers, acceptedId, "preparing"),
      ]) {
        expect(res.status()).toBe(409)
        expect(((await res.json()) as { code: string }).code).toBe("check_not_open")
      }

      const second = await request.post(`${POS}/checks/${checkId}/cancel`, { headers, data: {} })
      expect(second.status()).toBe(409)
    } finally {
      await cleanup(request, headers, checkId)
    }
  })

  test("mutfak siparişi kabul/ret/iptal edemez; advance yalnız mutfak adımlarını kabul eder", async ({ request }) => {
    const label = `E2E-MTF-${Date.now().toString(36)}`
    const manager = await headersFor(request, USERS.manager)
    const kitchen = await headersFor(request, USERS.kitchen)
    const checkId = await openCheck(request, manager, label)

    try {
      const pendingId = await placeOrder(request, manager, checkId, [1])
      const acceptedId = await placeOrder(request, manager, checkId, [1])
      await accept(request, manager, acceptedId)

      for (const action of ["accept", "reject", "cancel"]) {
        const res = await request.post(`${POS}/orders/${pendingId}/${action}`, { headers: kitchen, data: {} })
        expect(res.status(), `${action}: ${await res.text()}`).toBe(403)
      }

      // The 2026-09-19 escalation: the same transitions used to go through /advance.
      for (const [orderId, status] of [
        [pendingId, "accepted"],
        [pendingId, "rejected"],
        [acceptedId, "cancelled"],
      ]) {
        const res = await advance(request, kitchen, orderId, status)
        expect(res.status(), `${status}: ${await res.text()}`).toBe(422)
        expect(((await res.json()) as { code: string }).code).toBe("use_dedicated_endpoint")
      }

      const bogus = await advance(request, kitchen, acceptedId, "bogus")
      expect(bogus.status()).toBe(422)
      expect(((await bogus.json()) as { code: string }).code).toBe("invalid_status")

      expect((await getOrder(request, manager, pendingId)).status).toBe("pending")
      expect((await getOrder(request, manager, acceptedId)).status).toBe("accepted")

      const preparing = await advance(request, kitchen, acceptedId, "preparing")
      expect(preparing.status(), await preparing.text()).toBe(200)
      const body = (await preparing.json()) as Order & { items: unknown[] }
      expect(body.status).toBe("preparing")
      expect(body.items).toHaveLength(1)
    } finally {
      await cleanup(request, manager, checkId)
    }
  })

  test("garson kabul ve ret yetkisine sahip değil", async ({ request }) => {
    const label = `E2E-GRS-${Date.now().toString(36)}`
    const manager = await headersFor(request, USERS.manager)
    const waiter = await headersFor(request, USERS.waiter)
    const checkId = await openCheck(request, manager, label)

    try {
      const orderId = await placeOrder(request, manager, checkId, [1])

      const reject = await request.post(`${POS}/orders/${orderId}/reject`, {
        headers: waiter,
        data: { reason: "garson reddi" },
      })
      expect(reject.status()).toBe(403)

      const accept = await request.post(`${POS}/orders/${orderId}/accept`, { headers: waiter })
      expect(accept.status()).toBe(403)

      // The waiter holds no pos.order.* grant at all, not even read.
      const read = await request.get(`${POS}/orders/${orderId}`, { headers: waiter })
      expect(read.status()).toBe(403)

      expect((await getOrder(request, manager, orderId)).status).toBe("pending")
    } finally {
      await cleanup(request, manager, checkId)
    }
  })
})

// ---------------------------------------------------------------------------
// docs/pos-ux-spec.md §3c — masa taşıma / birleştirme / kalem taşıma
// ---------------------------------------------------------------------------

const MODIFIER_EXTRA_SAUCE = "dddddddd-0000-0000-0000-000000000311" // Ekstra sos, +₺15
const EXTRA_SAUCE_DELTA = 1500

async function emptyTable(request: APIRequestContext, headers: Headers): Promise<PosTable> {
  const res = await request.get(`${POS}/tables?branch_id=${BRANCH_ID}`, { headers })
  expect(res.status(), await res.text()).toBe(200)
  const zones = (await res.json()) as { tables: PosTable[] }[]
  const free = zones.flatMap((z) => z.tables).find((t) => t.status === "empty")
  expect(free, "dev seed must leave at least one empty table for the transfer test").toBeTruthy()
  return free as PosTable
}

// Cancelling a check parks its table in "cleaning", which the next run cannot
// transfer onto. Manager holds pos.table.manage, so the fixture resets it.
async function resetTable(request: APIRequestContext, headers: Headers, tableId: string) {
  await request.post(`${POS}/tables/${tableId}/status`, { headers, data: { status: "empty" } })
}

test.describe("adisyon taşıma, birleştirme ve kalem taşıma", () => {
  test("birleştirilen adisyon iptal değil 'merged' olur ve tutarı hedefe taşınır", async ({ request }) => {
    const headers = await headersFor(request, USERS.manager)
    const target = await openCheck(request, headers, `E2E-MRG-T-${Date.now().toString(36)}`)
    const source = await openCheck(request, headers, `E2E-MRG-S-${Date.now().toString(36)}`)

    try {
      await placeOrder(request, headers, target, [1])
      await placeOrder(request, headers, source, [2])
      expect((await getCheck(request, headers, target)).total).toBe(UNIT_PRICE)
      expect((await getCheck(request, headers, source)).total).toBe(2 * UNIT_PRICE)

      const key = `e2e-merge-${randomUUID()}`
      const merge = await request.post(`${POS}/checks/${target}/merge`, {
        headers: { ...headers, "Idempotency-Key": key },
        data: { source_check_id: source },
      })
      expect(merge.status(), await merge.text()).toBe(200)

      expect((await getCheck(request, headers, target)).total).toBe(3 * UNIT_PRICE)

      const merged = await getCheck(request, headers, source)
      expect(merged.status, "a merged adisyon must not read as a cancelled sale").toBe("merged")
      expect(merged.merged_into_check_id).toBe(target)
      expect(merged.total).toBe(0)

      // Same key, same body: the middleware replays instead of merging twice.
      const replay = await request.post(`${POS}/checks/${target}/merge`, {
        headers: { ...headers, "Idempotency-Key": key },
        data: { source_check_id: source },
      })
      expect(replay.status()).toBe(200)
      expect(replay.headers()["idempotency-replayed"]).toBe("true")
      expect((await getCheck(request, headers, target)).total).toBe(3 * UNIT_PRICE)

      // A second, genuinely new attempt finds the source no longer open.
      const again = await request.post(`${POS}/checks/${target}/merge`, {
        headers: { ...headers, "Idempotency-Key": `e2e-merge-${randomUUID()}` },
        data: { source_check_id: source },
      })
      expect(again.status()).toBe(409)
      expect(((await again.json()) as { code: string }).code).toBe("check_not_open")
    } finally {
      await cleanup(request, headers, target)
    }
  })

  test("kalem taşıma seçilen satırın tutarını hedef adisyona geçirir", async ({ request }) => {
    const headers = await headersFor(request, USERS.manager)
    const source = await openCheck(request, headers, `E2E-MVS-${Date.now().toString(36)}`)
    const target = await openCheck(request, headers, `E2E-MVT-${Date.now().toString(36)}`)

    try {
      const stays = await placeOrderFull(request, headers, source, [1])
      const moves = await placeOrderFull(request, headers, source, [2])
      expect((await getCheck(request, headers, source)).total).toBe(3 * UNIT_PRICE)

      const move = await request.post(`${POS}/checks/${source}/move-items`, {
        headers: { ...headers, "Idempotency-Key": `e2e-move-${randomUUID()}` },
        data: { target_check_id: target, order_item_ids: [moves.items[0].id] },
      })
      expect(move.status(), await move.text()).toBe(200)
      expect(((await move.json()) as Check).id, "the response is the target check").toBe(target)

      expect((await getCheck(request, headers, source)).total).toBe(UNIT_PRICE)
      expect((await getCheck(request, headers, target)).total).toBe(2 * UNIT_PRICE)

      // The emptied source order is cancelled; the untouched one is not.
      expect((await getOrder(request, headers, moves.id)).status).toBe("cancelled")
      expect((await getOrder(request, headers, stays.id)).status).toBe("pending")

      // A line that now belongs to the target can no longer be moved off the source.
      const foreign = await request.post(`${POS}/checks/${source}/move-items`, {
        headers: { ...headers, "Idempotency-Key": `e2e-move-${randomUUID()}` },
        data: { target_check_id: target, order_item_ids: [moves.items[0].id] },
      })
      expect(foreign.status()).toBe(422)
      expect(((await foreign.json()) as { code: string }).code).toBe("order_item_not_found")
    } finally {
      await cleanup(request, headers, source)
      await cleanup(request, headers, target)
    }
  })

  test("masa taşıma hedef masayı aynı istekte dolu yapar", async ({ request }) => {
    const headers = await headersFor(request, USERS.manager)
    const table = await emptyTable(request, headers)
    const checkId = await openCheck(request, headers, `E2E-TRF-${Date.now().toString(36)}`)

    try {
      await placeOrder(request, headers, checkId, [1])

      const transfer = await request.post(`${POS}/checks/${checkId}/transfer`, {
        headers: { ...headers, "Idempotency-Key": `e2e-transfer-${randomUUID()}` },
        data: { table_id: table.id },
      })
      expect(transfer.status(), await transfer.text()).toBe(200)
      const moved = (await transfer.json()) as Check
      expect(moved.table_id).toBe(table.id)
      expect(moved.table_label, "the label follows the table so the KDS agrees").toBe(table.name)

      // The cashier holds no pos.table.manage, so the server must have done this.
      const after = await emptyTable(request, headers)
      expect(after.id, "the target table is no longer empty").not.toBe(table.id)

      const unknown = await request.post(`${POS}/checks/${checkId}/transfer`, {
        headers: { ...headers, "Idempotency-Key": `e2e-transfer-${randomUUID()}` },
        data: { table_id: randomUUID() },
      })
      expect(unknown.status()).toBe(422)
      expect(((await unknown.json()) as { code: string }).code).toBe("table_not_found")
    } finally {
      await cleanup(request, headers, checkId)
      await resetTable(request, headers, table.id)
    }
  })

  test("kasiyer birleştirebilir, garson birleştiremez", async ({ request }) => {
    const manager = await headersFor(request, USERS.manager)
    const cashier = await headersFor(request, USERS.cashier)
    const waiter = await headersFor(request, USERS.waiter)
    const target = await openCheck(request, manager, `E2E-AZM-T-${Date.now().toString(36)}`)
    const source = await openCheck(request, manager, `E2E-AZM-S-${Date.now().toString(36)}`)

    try {
      const denied = await request.post(`${POS}/checks/${target}/merge`, {
        headers: { ...waiter, "Idempotency-Key": `e2e-merge-${randomUUID()}` },
        data: { source_check_id: source },
      })
      expect(denied.status(), "the waiter holds no pos.check.merge").toBe(403)

      const allowed = await request.post(`${POS}/checks/${target}/merge`, {
        headers: { ...cashier, "Idempotency-Key": `e2e-merge-${randomUUID()}` },
        data: { source_check_id: source },
      })
      expect(allowed.status(), await allowed.text()).toBe(200)
      expect((await getCheck(request, manager, source)).status).toBe("merged")
    } finally {
      await cleanup(request, manager, target)
    }
  })
})

// ---------------------------------------------------------------------------
// docs/pos-ux-spec.md bulgu #14 / P0 — sunucu tarafı fiyat doğrulaması
// ---------------------------------------------------------------------------

test.describe("sunucu tarafı fiyat doğrulaması", () => {
  test("manipüle edilmiş birim fiyat 422 price_mismatch ile reddedilir", async ({ request }) => {
    const headers = await headersFor(request, USERS.manager)
    const checkId = await openCheck(request, headers, `E2E-FYT-${Date.now().toString(36)}`)

    try {
      for (const sent of [1, UNIT_PRICE - 1, UNIT_PRICE + 1]) {
        const res = await request.post(`${POS}/orders`, {
          headers: { ...headers, "Idempotency-Key": `e2e-price-${randomUUID()}` },
          data: {
            branch_id: BRANCH_ID,
            check_id: checkId,
            order_channel: "dine_in",
            items: [{ ...item(1), unit_price_amount: sent }],
          },
        })
        expect(res.status(), `sent ${sent}: ${await res.text()}`).toBe(422)
        expect(((await res.json()) as { code: string }).code).toBe("price_mismatch")
      }
      expect((await getCheck(request, headers, checkId)).total, "nothing reached the adisyon").toBe(0)

      const unknown = await request.post(`${POS}/orders`, {
        headers: { ...headers, "Idempotency-Key": `e2e-price-${randomUUID()}` },
        data: {
          branch_id: BRANCH_ID,
          check_id: checkId,
          order_channel: "dine_in",
          items: [{ ...item(1), product_id: randomUUID() }],
        },
      })
      expect(unknown.status()).toBe(422)
      expect(((await unknown.json()) as { code: string }).code).toBe("invalid_order_line")
    } finally {
      await cleanup(request, headers, checkId)
    }
  })

  test("seçenek farkı sunucuda fiyata eklenir", async ({ request }) => {
    const headers = await headersFor(request, USERS.manager)
    const checkId = await openCheck(request, headers, `E2E-MOD-${Date.now().toString(36)}`)

    try {
      // Base price alone is wrong once an option with a delta is selected.
      const stale = await request.post(`${POS}/orders`, {
        headers: { ...headers, "Idempotency-Key": `e2e-mod-${randomUUID()}` },
        data: {
          branch_id: BRANCH_ID,
          check_id: checkId,
          order_channel: "dine_in",
          items: [{ ...item(1), modifier_ids: [MODIFIER_EXTRA_SAUCE] }],
        },
      })
      expect(stale.status()).toBe(422)
      expect(((await stale.json()) as { code: string }).code).toBe("price_mismatch")

      const ok = await request.post(`${POS}/orders`, {
        headers: { ...headers, "Idempotency-Key": `e2e-mod-${randomUUID()}` },
        data: {
          branch_id: BRANCH_ID,
          check_id: checkId,
          order_channel: "dine_in",
          items: [
            {
              ...item(2),
              unit_price_amount: UNIT_PRICE + EXTRA_SAUCE_DELTA,
              note: "Ekstra sos",
              modifier_ids: [MODIFIER_EXTRA_SAUCE],
            },
          ],
        },
      })
      expect(ok.status(), await ok.text()).toBe(201)
      expect((await getCheck(request, headers, checkId)).total).toBe(2 * (UNIT_PRICE + EXTRA_SAUCE_DELTA))
    } finally {
      await cleanup(request, headers, checkId)
    }
  })
})
