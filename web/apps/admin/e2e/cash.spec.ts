import { type APIRequestContext, type APIResponse, expect, test } from "@playwright/test"

import { API_URL, BRANCH_ID, PRODUCT_ID, USERS, devToken } from "./fixtures/auth"

// Cash-day scenario against the live dev stack (real pilot flow): drawer open →
// sales → wrong-order correction → movements → count with a shortfall → close,
// plus what happens when cash is taken with no open drawer. Every step reads
// the wire shape from the handlers, so a contract drift fails here first.

const RUN = Date.now().toString(36)
const OPENING_AMOUNT = 50_000
const SHORTFALL = 5_000

type Api = ReturnType<typeof apiFor>

interface CashSession {
  id: string
  status: string
  opening_counted_amount: number
  closing_counted_amount: number | null
  closed_by: string | null
  closed_at: string | null
  movements_net: number
  cash_payments_taken: number
  expected_close: number
  difference: number | null
}

interface Payment {
  id: string
  status: string
  method: string
  amount_total: number
  check_id: string | null
  fiscal_receipt_id: string | null
}

interface SaleDetails {
  sales: { closed_check_count: number; gross: number }
  payments: { method: string; status: string; count: number; total: number }[]
  cash_sessions: {
    id: string
    status: string
    cash_payments_taken: number
    movements_net: number
    expected_close: number
    closing_counted_amount: number | null
    difference: number | null
  }[]
}

function apiFor(request: APIRequestContext, token: string) {
  const headers = { Authorization: `Bearer ${token}` }
  const withKey = (key?: string) => (key ? { ...headers, "Idempotency-Key": key } : headers)
  return {
    get: (path: string) => request.get(`${API_URL}${path}`, { headers }),
    post: (path: string, data?: unknown, key?: string) =>
      request.post(`${API_URL}${path}`, { headers: withKey(key), data }),
  }
}

async function expectStatus(res: APIResponse, status: number): Promise<void> {
  expect(res.status(), `${res.url()} → ${await res.text()}`).toBe(status)
}

async function json<T>(res: APIResponse, status: number): Promise<T> {
  await expectStatus(res, status)
  return (await res.json()) as T
}

async function activeSession(api: Api): Promise<CashSession | null> {
  const res = await api.get(`/api/v1/payments/cash-sessions/active?branch_id=${BRANCH_ID}`)
  if (res.status() === 404) return null
  return json<CashSession>(res, 200)
}

async function openCheck(api: Api, label: string): Promise<string> {
  const check = await json<{ id: string }>(
    await api.post("/api/v1/pos/checks", { branch_id: BRANCH_ID, table_label: label, pax: 2 }),
    201,
  )
  return check.id
}

async function placeOrder(api: Api, checkId: string, name: string, price: number, key: string) {
  return api.post(
    "/api/v1/pos/orders",
    {
      branch_id: BRANCH_ID,
      check_id: checkId,
      order_channel: "dine_in",
      items: [
        {
          product_id: PRODUCT_ID,
          product_name: name,
          product_price_amount: price,
          product_currency: "TRY",
          tax_rate_bps: 1000,
          quantity: 1,
          unit_price_amount: price,
        },
      ],
    },
    key,
  )
}

async function placeOrderOk(api: Api, checkId: string, name: string, price: number, key: string): Promise<string> {
  const order = await json<{ id: string }>(await placeOrder(api, checkId, name, price, key), 201)
  return order.id
}

function cashSale(checkId: string, name: string, amount: number) {
  return {
    branch_id: BRANCH_ID,
    check_id: checkId,
    method: "cash",
    amount_total: amount,
    currency: "TRY",
    lines: [{ name, unit_price_minor: amount, quantity_milli: 1000, tax_rate_permyriad: 1000, unit: "C62" }],
  }
}

async function payCash(api: Api, checkId: string, name: string, amount: number, key: string): Promise<Payment> {
  return json<Payment>(await api.post("/api/v1/payments", cashSale(checkId, name, amount), key), 201)
}

// The fiscal mock answers after ~1s; the payment is created "pending" and the
// worker moves it to "completed". payment.payment.read is manager/shift only,
// so the poll runs as the manager while the cashier keeps the counter flow.
async function waitCompleted(reader: Api, paymentId: string): Promise<Payment> {
  await expect
    .poll(async () => (await json<Payment>(await reader.get(`/api/v1/payments/${paymentId}`), 200)).status, {
      timeout: 20_000,
      intervals: [500],
    })
    .toBe("completed")
  return json<Payment>(await reader.get(`/api/v1/payments/${paymentId}`), 200)
}

async function closeCheck(api: Api, checkId: string, key: string) {
  return api.post(`/api/v1/pos/checks/${checkId}/close`, undefined, key)
}

async function saleDetails(api: Api, from: string, to: string): Promise<SaleDetails> {
  const query = new URLSearchParams({ branch_id: BRANCH_ID, from, to })
  return json<SaleDetails>(await api.get(`/api/v1/pos/reports/sale-details?${query}`), 200)
}

function cashTotal(report: SaleDetails, status: string): { count: number; total: number } {
  const row = report.payments.find((p) => p.method === "cash" && p.status === status)
  return { count: row?.count ?? 0, total: row?.total ?? 0 }
}

test.describe.configure({ mode: "serial" })

test.describe("kasa günü", () => {
  let cashier: Api
  let manager: Api
  let waiter: Api
  let disposeContext: () => Promise<void>

  const windowFrom = new Date(Date.now() - 60_000).toISOString()
  const windowTo = new Date(Date.now() + 3_600_000).toISOString()

  let baseline: SaleDetails
  let sessionId: string
  const openedChecks: string[] = []

  test.beforeAll(async ({ playwright }) => {
    const context = await playwright.request.newContext()
    disposeContext = () => context.dispose()
    const [c, m, w] = await Promise.all([
      devToken(context, USERS.cashier),
      devToken(context, USERS.manager),
      devToken(context, USERS.waiter),
    ])
    cashier = apiFor(context, c.token)
    manager = apiFor(context, m.token)
    waiter = apiFor(context, w.token)
  })

  test.afterAll(async () => {
    // A failed run must not leave the shared branch with an open drawer or
    // dangling adisyons for the next spec (best-effort; assertions above are
    // what fail the test).
    for (const checkId of openedChecks) {
      await manager.post(`/api/v1/pos/checks/${checkId}/cancel`, {})
    }
    const leftover = await activeSession(manager).catch(() => null)
    if (leftover) {
      await manager.post(`/api/v1/payments/cash-sessions/${leftover.id}/closing-count`, {
        closing_counted_amount: leftover.expected_close,
      })
      await manager.post(`/api/v1/payments/cash-sessions/${leftover.id}/close`)
    }
    await disposeContext()
  })

  test("önceki koşudan kalan oturum kapatılır; kasiyer kasa açar, garson açamaz, ikinci açış 409", async () => {
    baseline = await saleDetails(manager, windowFrom, windowTo)

    const leftover = await activeSession(manager)
    if (leftover) {
      await json<CashSession>(
        await manager.post(`/api/v1/payments/cash-sessions/${leftover.id}/closing-count`, {
          closing_counted_amount: leftover.expected_close,
          notes: "e2e: önceki koşudan kalan oturum",
        }),
        200,
      )
      const closed = await json<CashSession>(await manager.post(`/api/v1/payments/cash-sessions/${leftover.id}/close`), 200)
      expect(closed.status).toBe("closed")
    }
    expect(await activeSession(manager)).toBeNull()

    // Waiter holds no payment.cash_session.* permission at all: 403 on both
    // the read and the open, and nothing gets created by the attempt.
    await expectStatus(await waiter.get(`/api/v1/payments/cash-sessions/active?branch_id=${BRANCH_ID}`), 403)
    await expectStatus(
      await waiter.post("/api/v1/payments/cash-sessions", {
        branch_id: BRANCH_ID,
        opening_counted_amount: OPENING_AMOUNT,
      }),
      403,
    )
    expect(await activeSession(manager)).toBeNull()

    const opened = await json<CashSession>(
      await cashier.post("/api/v1/payments/cash-sessions", {
        branch_id: BRANCH_ID,
        opening_counted_amount: OPENING_AMOUNT,
        opening_notes: `e2e-${RUN}`,
      }),
      201,
    )
    sessionId = opened.id
    expect(opened).toMatchObject({
      status: "opened",
      opening_counted_amount: OPENING_AMOUNT,
      movements_net: 0,
      cash_payments_taken: 0,
      expected_close: OPENING_AMOUNT,
      difference: null,
    })

    await expectStatus(
      await manager.post("/api/v1/payments/cash-sessions", {
        branch_id: BRANCH_ID,
        opening_counted_amount: OPENING_AMOUNT,
      }),
      409,
    )
    await expectStatus(
      await cashier.post("/api/v1/payments/cash-sessions", {
        branch_id: BRANCH_ID,
        opening_counted_amount: -1,
      }),
      422,
    )
    expect((await activeSession(cashier))?.id).toBe(sessionId)
  })

  test("adisyon → sipariş → nakit ödeme → fiş → adisyon kapanır; satış kasa toplamına yansır", async () => {
    const saleCheckId = await openCheck(cashier, `E2E-K1-${RUN}`)
    openedChecks.push(saleCheckId)
    const orderId = await placeOrderOk(cashier, saleCheckId, "Adana Kebap", 32_000, `e2e-${RUN}-k1-order`)
    await json(await cashier.post(`/api/v1/pos/orders/${orderId}/accept`), 200)

    // Money is not collected yet: closing the adisyon must be refused.
    const early = await closeCheck(cashier, saleCheckId, `e2e-${RUN}-k1-close-early`)
    await expectStatus(early, 409)
    expect(await early.json()).toMatchObject({ code: "insufficient_payment" })

    // Idempotency-Key is mandatory on payments (ADR-SEC-003).
    await expectStatus(await cashier.post("/api/v1/payments", cashSale(saleCheckId, "Adana Kebap", 32_000)), 422)

    const before = await activeSession(cashier)
    const paymentKey = `e2e-${RUN}-k1-pay`
    const payment = await payCash(cashier, saleCheckId, "Adana Kebap", 32_000, paymentKey)
    expect(payment).toMatchObject({ method: "cash", amount_total: 32_000, check_id: saleCheckId })
    expect(payment.status).toBe("pending")

    // A retry with the same key returns the same payment instead of a second charge.
    const replay = await cashier.post("/api/v1/payments", cashSale(saleCheckId, "Adana Kebap", 32_000), paymentKey)
    expect((await json<Payment>(replay, 201)).id).toBe(payment.id)

    const completed = await waitCompleted(manager, payment.id)
    expect(completed.fiscal_receipt_id).not.toBeNull()

    // A paid adisyon cannot be cancelled away: the registered sale would be
    // left pointing at a check that claims nothing was sold.
    const paidCancel = await cashier.post(`/api/v1/pos/checks/${saleCheckId}/cancel`, {})
    await expectStatus(paidCancel, 409)
    expect(await paidCancel.json()).toMatchObject({ code: "check_has_payments" })

    // The cashier cannot read payments; the check-scoped settlement is their view.
    const settlement = await json<{ completed: { payment_id: string; amount_total: number }[]; pending_total: number }>(
      await cashier.get(`/api/v1/payments/checks/${saleCheckId}/settlement`),
      200,
    )
    expect(settlement.completed).toEqual([{ payment_id: payment.id, amount_total: 32_000 }])
    expect(settlement.pending_total).toBe(0)

    // Payments carry no cash_session_id: the drawer figure is a branch-wide sum of
    // completed cash payments since the session opened (see SumCompletedCashPayments).
    const after = await activeSession(cashier)
    expect(after!.cash_payments_taken - before!.cash_payments_taken).toBe(32_000)
    expect(after!.expected_close - before!.expected_close).toBe(32_000)

    const closed = await json<{ status: string; closed_at: string | null }>(
      await closeCheck(cashier, saleCheckId, `e2e-${RUN}-k1-close`),
      200,
    )
    expect(closed.status).toBe("closed")
    expect(closed.closed_at).not.toBeNull()

    // Closed adisyon takes neither orders nor money (2026-09-15 production finding).
    const lateOrder = await placeOrder(cashier, saleCheckId, "Ayran", 3_000, `e2e-${RUN}-k1-late-order`)
    await expectStatus(lateOrder, 409)
    expect(await lateOrder.json()).toMatchObject({ code: "check_not_open" })
    const latePay = await cashier.post("/api/v1/payments", cashSale(saleCheckId, "Ayran", 3_000), `e2e-${RUN}-k1-late-pay`)
    await expectStatus(latePay, 409)
    expect(await latePay.json()).toMatchObject({ code: "check_not_open" })
  })

  test("yanlış sipariş reddedilir, doğrusu verilir; eksik ödemeyle kapanmaz, tam ödemeyle 200", async () => {
    const correctionCheckId = await openCheck(cashier, `E2E-K2-${RUN}`)
    openedChecks.push(correctionCheckId)

    const wrongId = await placeOrderOk(cashier, correctionCheckId, "Yanlış Sipariş", 25_000, `e2e-${RUN}-k2-wrong`)
    const rejected = await json<{ status: string }>(
      await cashier.post(`/api/v1/pos/orders/${wrongId}/reject`, { reason: "e2e: yanlış girildi" }),
      200,
    )
    expect(rejected.status).toBe("rejected")

    const rightId = await placeOrderOk(cashier, correctionCheckId, "İskender", 18_000, `e2e-${RUN}-k2-right`)
    await json(await cashier.post(`/api/v1/pos/orders/${rightId}/accept`), 200)

    // The rejected order stays in the ledger but never counts toward the total.
    const orders = await json<{ id: string; status: string }[]>(
      await cashier.get(`/api/v1/pos/checks/${correctionCheckId}/orders`),
      200,
    )
    expect(orders.find((o) => o.id === wrongId)?.status).toBe("rejected")
    const check = await json<{ total: number }>(await cashier.get(`/api/v1/pos/checks/${correctionCheckId}`), 200)
    expect(check.total).toBe(18_000)

    // A rejected order cannot be rejected again.
    await expectStatus(await cashier.post(`/api/v1/pos/orders/${wrongId}/reject`, { reason: "e2e: tekrar" }), 409)

    const noPayment = await closeCheck(cashier, correctionCheckId, `e2e-${RUN}-k2-close-none`)
    await expectStatus(noPayment, 409)
    expect(await noPayment.json()).toMatchObject({ code: "insufficient_payment" })

    const partial = await payCash(cashier, correctionCheckId, "İskender (kısmi)", 10_000, `e2e-${RUN}-k2-pay-1`)
    await waitCompleted(manager, partial.id)
    const underpaid = await closeCheck(cashier, correctionCheckId, `e2e-${RUN}-k2-close-partial`)
    await expectStatus(underpaid, 409)
    expect(await underpaid.json()).toMatchObject({ code: "insufficient_payment" })

    const rest = await payCash(cashier, correctionCheckId, "İskender (kalan)", 8_000, `e2e-${RUN}-k2-pay-2`)
    await waitCompleted(manager, rest.id)
    const closed = await json<{ status: string }>(await closeCheck(cashier, correctionCheckId, `e2e-${RUN}-k2-close`), 200)
    expect(closed.status).toBe("closed")

    // Only the correct amount was collected: 32 000 + 18 000 in the drawer.
    const session = await activeSession(cashier)
    expect(session!.cash_payments_taken).toBeGreaterThanOrEqual(50_000)
  })

  test("kasa hareketleri: para çıkışı ve girişi deftere yazılır, bakiyeyi etkiler", async () => {
    const path = `/api/v1/payments/cash-sessions/${sessionId}/movements`
    const before = await activeSession(cashier)

    const out = await json<{ direction: string; amount_minor: number; reason: string; session_id: string }>(
      await cashier.post(path, { direction: "out", amount_minor: 10_000, reason: "e2e: tedarikçiye avans" }, `e2e-${RUN}-mv-out`),
      201,
    )
    expect(out).toMatchObject({ direction: "out", amount_minor: 10_000, session_id: sessionId })

    // Same key → same movement, drawer is not debited twice.
    const replay = await cashier.post(path, { direction: "out", amount_minor: 10_000, reason: "e2e: tedarikçiye avans" }, `e2e-${RUN}-mv-out`)
    await expectStatus(replay, 201)

    await json(
      await cashier.post(path, { direction: "in", amount_minor: 2_500, reason: "e2e: bozuk para" }, `e2e-${RUN}-mv-in`),
      201,
    )

    // Validation: bad direction / amount / missing reason → 422; no key → 422.
    await expectStatus(await cashier.post(path, { direction: "sideways", amount_minor: 100, reason: "x" }, `e2e-${RUN}-mv-bad-dir`), 422)
    await expectStatus(await cashier.post(path, { direction: "out", amount_minor: 0, reason: "x" }, `e2e-${RUN}-mv-bad-amt`), 422)
    await expectStatus(await cashier.post(path, { direction: "out", amount_minor: 100, reason: "  " }, `e2e-${RUN}-mv-bad-reason`), 422)
    await expectStatus(await cashier.post(path, { direction: "out", amount_minor: 100, reason: "x" }), 422)

    const ledger = await json<{ direction: string; amount_minor: number; reason: string }[]>(await cashier.get(path), 200)
    expect(ledger.map((m) => [m.direction, m.amount_minor])).toEqual([
      ["out", 10_000],
      ["in", 2_500],
    ])

    const after = await activeSession(cashier)
    expect(after!.movements_net - before!.movements_net).toBe(-7_500)
    expect(after!.expected_close).toBe(after!.opening_counted_amount + after!.cash_payments_taken + after!.movements_net)
  })

  test("kapanış: beklenenden 5000 eksik sayım fark olarak raporlanır; kapatınca oturum biter", async () => {
    const live = await activeSession(cashier)
    const expected = live!.expected_close
    expect(expected).toBe(OPENING_AMOUNT + live!.cash_payments_taken - 7_500)

    // Closing before any count is refused: the drawer must be counted first.
    await expectStatus(await cashier.post(`/api/v1/payments/cash-sessions/${sessionId}/close`), 409)

    // A denomination breakdown that disagrees with the total is rejected.
    await expectStatus(
      await cashier.post(`/api/v1/payments/cash-sessions/${sessionId}/closing-count`, {
        closing_counted_amount: expected - SHORTFALL,
        denominations: [{ denomination_minor: 20_000, count: 1 }],
      }),
      422,
    )

    const counted = await json<CashSession>(
      await cashier.post(`/api/v1/payments/cash-sessions/${sessionId}/closing-count`, {
        closing_counted_amount: expected - SHORTFALL,
        notes: "e2e: bilinçli eksik sayım",
      }),
      200,
    )
    expect(counted).toMatchObject({
      status: "closing_control",
      closing_counted_amount: expected - SHORTFALL,
      expected_close: expected,
      difference: -SHORTFALL,
    })

    // The drawer is frozen once counted: a late movement would invalidate the count.
    await expectStatus(
      await cashier.post(
        `/api/v1/payments/cash-sessions/${sessionId}/movements`,
        { direction: "out", amount_minor: 100, reason: "e2e: geç hareket" },
        `e2e-${RUN}-mv-late`,
      ),
      409,
    )

    const closed = await json<CashSession>(await cashier.post(`/api/v1/payments/cash-sessions/${sessionId}/close`), 200)
    expect(closed).toMatchObject({ status: "closed", difference: -SHORTFALL, expected_close: expected })
    expect(closed.closed_by).not.toBeNull()
    expect(closed.closed_at).not.toBeNull()

    await expectStatus(await cashier.get(`/api/v1/payments/cash-sessions/active?branch_id=${BRANCH_ID}`), 404)
    await expectStatus(await cashier.post(`/api/v1/payments/cash-sessions/${sessionId}/close`), 409)
  })

  test("kasa kapalıyken nakit ödeme reddedilir (409); nakit dışı yöntem kasaya bağlı değildir", async () => {
    expect(await activeSession(manager)).toBeNull()

    const checkId = await openCheck(cashier, `E2E-K3-${RUN}`)
    openedChecks.push(checkId)
    await placeOrderOk(cashier, checkId, "Lahmacun", 12_000, `e2e-${RUN}-k3-order`)

    const blocked = await cashier.post("/api/v1/payments", cashSale(checkId, "Lahmacun", 12_000), `e2e-${RUN}-k3-pay`)
    await expectStatus(blocked, 409)
    expect(await blocked.text()).toContain("açık kasa oturumu yok")

    // Nothing was recorded against the check.
    const settlement = await json<{ completed: unknown[]; pending_total: number }>(
      await cashier.get(`/api/v1/payments/checks/${checkId}/settlement`),
      200,
    )
    expect(settlement).toMatchObject({ completed: [], pending_total: 0 })
    await expectStatus(await closeCheck(cashier, checkId, `e2e-${RUN}-k3-close`), 409)

    // The refusal is about the drawer: a card payment (ÖKC rail) needs no session.
    const card = await json<Payment>(
      await cashier.post(
        "/api/v1/payments",
        { ...cashSale(checkId, "Lahmacun", 12_000), method: "terminal" },
        `e2e-${RUN}-k3-card`,
      ),
      201,
    )
    await waitCompleted(manager, card.id)
    const closed = await json<{ status: string }>(await closeCheck(cashier, checkId, `e2e-${RUN}-k3-close-card`), 200)
    expect(closed.status).toBe("closed")
    openedChecks.pop()
  })

  test("gün sonu raporu: bugünün satışı ve kasa oturumu doğru, reddedilen sipariş dahil değil", async () => {
    const report = await saleDetails(manager, windowFrom, windowTo)

    // Other specs share the branch, so the assertions are deltas against the
    // report read before this file created anything.
    // Closed: K1 (32 000), K2 (18 000; the 25 000 rejected order is excluded),
    // K3 (12 000 paid by card).
    expect(report.sales.closed_check_count - baseline.sales.closed_check_count).toBeGreaterThanOrEqual(3)
    expect(report.sales.gross - baseline.sales.gross).toBe(32_000 + 18_000 + 12_000)

    const cashBefore = cashTotal(baseline, "completed")
    const cashAfter = cashTotal(report, "completed")
    expect(cashAfter.total - cashBefore.total).toBe(50_000)
    expect(cashAfter.count - cashBefore.count).toBe(3)
    const cardBefore = baseline.payments.find((p) => p.method === "terminal" && p.status === "completed")
    const cardAfter = report.payments.find((p) => p.method === "terminal" && p.status === "completed")
    expect((cardAfter?.total ?? 0) - (cardBefore?.total ?? 0)).toBe(12_000)

    // The refused cash attempt (K3) left no failed/pending cash row behind.
    expect(cashTotal(report, "pending")).toEqual(cashTotal(baseline, "pending"))

    const session = report.cash_sessions.find((s) => s.id === sessionId)
    expect(session).toMatchObject({
      status: "closed",
      cash_payments_taken: 50_000,
      movements_net: -7_500,
      expected_close: OPENING_AMOUNT + 50_000 - 7_500,
      closing_counted_amount: OPENING_AMOUNT + 50_000 - 7_500 - SHORTFALL,
      difference: -SHORTFALL,
    })

    // The cashier does not see the day report.
    const query = new URLSearchParams({ branch_id: BRANCH_ID, from: windowFrom, to: windowTo })
    await expectStatus(await cashier.get(`/api/v1/pos/reports/sale-details?${query}`), 403)
  })
})
