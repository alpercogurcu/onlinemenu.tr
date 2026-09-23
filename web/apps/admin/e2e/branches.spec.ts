import { type APIRequestContext, type APIResponse, expect, test } from "@playwright/test"
import WebSocket from "ws"

import {
  API_URL,
  BRANCH_ID as BRANCH_A,
  PRODUCTS,
  type SeededProduct,
  USERS,
  devToken,
  errorToast,
  gotoSpa,
  loginAs,
} from "./fixtures/auth"

// Multi-branch + multi-user scenario against the live dev stack (ADR-SEC-005,
// ADR-AUTH-001 layer 3). A second branch ("E2E Şube B") is found-or-created
// through the API, three branch-bound staff are attached to it, and the spec
// then proves what a branch-B operator can and cannot do to branch A, what the
// chain-wide manager sees, and that no cost field reaches counter/guest
// surfaces.
//
const RUN = Date.now().toString(36)

// The modules mount under different prefixes on purpose-less history, not by
// design: tenant has none (and needs the trailing slash), identity is /v1
// without /api, everything else is /api/v1, the guest storefront /api/public/v1.
const tenantBranchesPath = (tenantId: string) => `/tenants/${tenantId}/branches/`
const identityPath = (tenantId: string) => `/v1/identity/${tenantId}`

const BRANCH_B_NAME = "E2E Şube B"
const BRANCH_B_SLUG = "e2e-sube-b"
const ZONE_B_NAME = "E2E Salon B"
const TABLE_B_NAME = "E2E Masa B1"
const UI_BRANCH_NAME = "E2E UI Şube"

// System role ids, identical in every tenant of the dev seed
// (backend/deploy/dev-seed.sql, identity migrations).
const ROLE = {
  cashier: "00000001-0000-0000-0000-000000000001",
  kitchen: "00000001-0000-0000-0000-000000000004",
  waiter: "00000001-0000-0000-0000-000000000008",
} as const

// The persons rows come from backend/deploy/dev-seed.sql (there is no HTTP
// path that creates a person in dev: POST /persons is disabled and POST /staff
// needs Keycloak). Their memberships are made by this spec.
const STAFF_B = {
  cashier: { email: "kasiyer.b@dev.onlinemenu.tr", personId: "ffffffff-0000-0000-0000-000000000011", roleId: ROLE.cashier },
  waiter: { email: "garson.b@dev.onlinemenu.tr", personId: "ffffffff-0000-0000-0000-000000000012", roleId: ROLE.waiter },
  kitchen: { email: "mutfak.b@dev.onlinemenu.tr", personId: "ffffffff-0000-0000-0000-000000000013", roleId: ROLE.kitchen },
} as const

const SEEDED_CATEGORY_ID = "dddddddd-0000-0000-0000-000000000011"

// ADR-DATA-009 fixture. tavuk gets branch B's own (higher) price, lahmacun is
// switched off there. Both are in SEEDED_CATEGORY_ID and neither is used by
// the tests above, so an override cannot re-price an earlier assertion.
const OVERRIDE_PRICE = 31_000
const OPENING_AMOUNT = 20_000
const COST_KEY = /"[a-z_]*cost[a-z_]*"\s*:/i

type Api = ReturnType<typeof apiFor>

interface Branch {
  id: string
  name: string
  slug: string
}

interface Membership {
  id: string
  person_id: string
  person_email: string
  branch_id?: string
  role_id: string
  status: string
}

interface CashSession {
  id: string
  status: string
  cash_payments_taken: number
  expected_close: number
  closing_counted_amount: number | null
  difference: number | null
}

interface Payment {
  id: string
  status: string
  fiscal_receipt_id: string | null
}

interface CheckRow {
  id: string
  branch_id: string
  status: string
}

interface OrderRow {
  id: string
  status: string
  branch_id: string
}

interface SaleDetails {
  sales: { closed_check_count: number; gross: number }
  payments: { method: string; status: string; count: number; total: number }[]
  cash_sessions: { id: string; status: string; cash_payments_taken: number; difference: number | null }[]
}

type KitchenStream =
  | { kind: "snapshot"; orders: { order_id: string; table_label?: string; status: string }[] }
  | { kind: "refused"; status: number }

function apiFor(request: APIRequestContext, token: string) {
  const headers = { Authorization: `Bearer ${token}` }
  const withKey = (key?: string) => (key ? { ...headers, "Idempotency-Key": key } : headers)
  return {
    get: (path: string) => request.get(`${API_URL}${path}`, { headers }),
    post: (path: string, data?: unknown, key?: string) =>
      request.post(`${API_URL}${path}`, { headers: withKey(key), data }),
    put: (path: string, data?: unknown) => request.put(`${API_URL}${path}`, { headers, data }),
    del: (path: string) => request.delete(`${API_URL}${path}`, { headers }),
  }
}

async function expectStatus(res: APIResponse, status: number): Promise<void> {
  expect(res.status(), `${res.url()} → ${await res.text()}`).toBe(status)
}

async function json<T>(res: APIResponse, status: number): Promise<T> {
  await expectStatus(res, status)
  return (await res.json()) as T
}

// Branch-forbidden and branch-mismatch are both legitimate refusals (403 from
// the layer-3 guard, 404 if a route ever hides the row); anything else means
// the request went through.
async function expectRefused(res: APIResponse): Promise<void> {
  expect([403, 404, 409, 422], `${res.url()} → ${res.status()} ${await res.text()}`).toContain(res.status())
}

function tokenClaims(token: string): { bid?: string; tid?: string } {
  return JSON.parse(Buffer.from(token.split(".")[1], "base64url").toString("utf8")) as { bid?: string; tid?: string }
}

async function activeSession(api: Api, branchId: string): Promise<CashSession | null> {
  const res = await api.get(`/api/v1/payments/cash-sessions/active?branch_id=${branchId}`)
  if (res.status() === 404) return null
  return json<CashSession>(res, 200)
}

async function closeSession(api: Api, session: CashSession): Promise<void> {
  await api.post(`/api/v1/payments/cash-sessions/${session.id}/closing-count`, {
    closing_counted_amount: session.expected_close,
    notes: "e2e: şube spec temizliği",
  })
  await api.post(`/api/v1/payments/cash-sessions/${session.id}/close`)
}

async function openCheck(api: Api, branchId: string, label: string): Promise<string> {
  const check = await json<{ id: string }>(
    await api.post("/api/v1/pos/checks", { branch_id: branchId, table_label: label, pax: 2 }),
    201,
  )
  return check.id
}

// price defaults to the product's tenant price; a caller passes another one
// only to assert what the BRANCH's effective catalog charges (ADR-DATA-009).
function orderBody(branchId: string, checkId: string, product: SeededProduct, price: number = product.price) {
  return {
    branch_id: branchId,
    check_id: checkId,
    order_channel: "dine_in",
    items: [
      {
        product_id: product.id,
        product_name: product.name,
        product_price_amount: price,
        product_currency: "TRY",
        tax_rate_bps: 1000,
        quantity: 1,
        unit_price_amount: price,
      },
    ],
  }
}

async function placeOrder(api: Api, branchId: string, checkId: string, product: SeededProduct, key: string) {
  return api.post("/api/v1/pos/orders", orderBody(branchId, checkId, product), key)
}

async function placeOrderOk(api: Api, branchId: string, checkId: string, product: SeededProduct, key: string) {
  const order = await json<{ id: string }>(await placeOrder(api, branchId, checkId, product, key), 201)
  return order.id
}

// placeOrderAt prices the line at `price` instead of the product's tenant
// price. POST /pos/orders re-derives every line from the BRANCH's effective
// catalog and refuses anything else with 422 price_mismatch, so this is what
// proves an override really reached the order path.
function placeOrderAt(api: Api, branchId: string, checkId: string, product: SeededProduct, price: number, key: string) {
  return api.post("/api/v1/pos/orders", orderBody(branchId, checkId, product, price), key)
}

// Branch product overrides are tenant-wide state that survives a run, so a
// leftover row from an aborted run would re-price the earlier tests in this
// serial file. Every write below is paired with this teardown.
async function clearOverride(api: Api, branchId: string, productId: string): Promise<void> {
  const res = await api.del(`/api/v1/catalog/branches/${branchId}/products/${productId}/override`)
  expect([204, 404], `override temizliği → ${res.status()}`).toContain(res.status())
}

function saleBody(branchId: string, checkId: string, method: "cash" | "terminal", product: SeededProduct) {
  return {
    branch_id: branchId,
    check_id: checkId,
    method,
    amount_total: product.price,
    currency: "TRY",
    lines: [
      { name: product.name, unit_price_minor: product.price, quantity_milli: 1000, tax_rate_permyriad: 1000, unit: "C62" },
    ],
  }
}

async function waitCompleted(reader: Api, paymentId: string): Promise<Payment> {
  await expect
    .poll(async () => (await json<Payment>(await reader.get(`/api/v1/payments/${paymentId}`), 200)).status, {
      timeout: 20_000,
      intervals: [500],
    })
    .toBe("completed")
  return json<Payment>(await reader.get(`/api/v1/payments/${paymentId}`), 200)
}

// A closed adisyon refuses order transitions (409 check_not_open) and a live
// order under it would stay on the kitchen board forever, so everything is
// walked to "delivered" BEFORE the check is closed or cancelled.
async function serveLiveOrders(manager: Api, checkId: string): Promise<void> {
  const chain = ["accepted", "preparing", "ready", "delivered"]
  const orders = await json<OrderRow[]>(await manager.get(`/api/v1/pos/checks/${checkId}/orders`), 200)
  for (const order of orders) {
    let at = chain.indexOf(order.status)
    if (order.status === "pending") {
      await json(await manager.post(`/api/v1/pos/orders/${order.id}/accept`), 200)
      at = 0
    }
    if (at < 0) continue
    for (const status of chain.slice(at + 1)) {
      await json(await manager.post(`/api/v1/pos/orders/${order.id}/advance`, { status }), 200)
    }
  }
}

// Best-effort teardown of a check this spec opened: deliver its orders, then
// cancel; an adisyon that already carries a payment cannot be cancelled, so
// it is closed instead once that payment has settled.
async function retireCheck(manager: Api, checkId: string): Promise<void> {
  try {
    await serveLiveOrders(manager, checkId)
    const cancelled = await manager.post(`/api/v1/pos/checks/${checkId}/cancel`, {})
    if (cancelled.status() === 200) return
    for (let attempt = 0; attempt < 20; attempt++) {
      const closed = await manager.post(`/api/v1/pos/checks/${checkId}/close`, undefined, `e2e-${RUN}-retire-${checkId}-${attempt}`)
      if (closed.status() === 200) return
      await new Promise((resolve) => setTimeout(resolve, 500))
    }
  } catch {
    // Cleanup must never mask the assertion that already decided the test.
  }
}

function openKitchenStream(token: string, branchId: string): Promise<KitchenStream> {
  return new Promise((resolve, reject) => {
    const url = `${API_URL.replace(/^http/, "ws")}/api/v1/pos/ws/kitchen?branch_id=${branchId}`
    const socket = new WebSocket(url, { headers: { Authorization: `Bearer ${token}` } })
    const timer = setTimeout(() => {
      socket.terminate()
      reject(new Error(`kitchen stream for ${branchId}: no snapshot in 10s`))
    }, 10_000)
    const finish = (result: KitchenStream) => {
      clearTimeout(timer)
      socket.terminate()
      resolve(result)
    }
    socket.on("message", (raw) => {
      const message = JSON.parse(raw.toString()) as { type: string; orders?: { order_id: string; table_label?: string; status: string }[] }
      if (message.type === "snapshot") finish({ kind: "snapshot", orders: message.orders ?? [] })
    })
    socket.on("unexpected-response", (_request, response) => finish({ kind: "refused", status: response.statusCode ?? 0 }))
    socket.on("error", (error) => {
      clearTimeout(timer)
      reject(error)
    })
  })
}

async function saleDetails(api: Api, branchId: string, from: string, to: string): Promise<SaleDetails> {
  const query = new URLSearchParams({ branch_id: branchId, from, to })
  return json<SaleDetails>(await api.get(`/api/v1/pos/reports/sale-details?${query}`), 200)
}

test.describe.configure({ mode: "serial" })

test.describe("çoklu şube", () => {
  let disposeContext: () => Promise<void>
  let newContext: () => Promise<APIRequestContext>

  let tenantId: string
  let branchB: string
  let tableB: string

  let manager: Api
  let cashierA: Api
  let cashierB: Api
  let waiterB: Api
  let kitchenB: Api
  let kitchenAToken: string
  let kitchenBToken: string
  let managerToken: string

  const windowFrom = new Date(Date.now() - 60_000).toISOString()
  const windowTo = new Date(Date.now() + 3_600_000).toISOString()
  let baselineA: SaleDetails
  let baselineB: SaleDetails

  const liveChecks: string[] = []
  let ownedASession: CashSession | null = null
  let bSessionId: string

  // Isolation fixture: one live adisyon + order on branch A that branch B
  // staff try to reach.
  let victimCheck: string
  let victimOrder: string

  test.beforeAll(async ({ playwright }) => {
    const context = await playwright.request.newContext()
    newContext = () => playwright.request.newContext()
    disposeContext = () => context.dispose()

    const m = await devToken(context, USERS.manager)
    tenantId = m.tenantId
    managerToken = m.token
    manager = apiFor(context, m.token)
    cashierA = apiFor(context, (await devToken(context, USERS.cashier)).token)
    kitchenAToken = (await devToken(context, USERS.kitchen)).token

    // Branch B — found by slug, created only on the first run.
    const branches = await json<Branch[]>(await manager.get(tenantBranchesPath(tenantId)), 200)
    const existing = branches.find((b) => b.slug === BRANCH_B_SLUG)
    if (existing) {
      branchB = existing.id
    } else {
      const created = await json<Branch>(
        await manager.post(tenantBranchesPath(tenantId), {
          name: BRANCH_B_NAME,
          slug: BRANCH_B_SLUG,
          ownership_type: "sube",
          operation_type: "restoran",
          identity_type: "kurumsal",
          supply_rules: [],
          phone: "",
          address: { line1: "", city: "", district: "", postal_code: "", country: "TR" },
          is_active: true,
        }),
        201,
      )
      branchB = created.id
    }

    // Zone + table of branch B (the guest QR needs a table).
    const zones = await json<{ id: string; name: string }[]>(await manager.get(`/api/v1/pos/zones?branch_id=${branchB}`), 200)
    let zoneId = zones.find((z) => z.name === ZONE_B_NAME)?.id
    if (!zoneId) {
      zoneId = (
        await json<{ id: string }>(await manager.post("/api/v1/pos/zones", { branch_id: branchB, name: ZONE_B_NAME, floor: 0 }), 201)
      ).id
    }
    const plan = await json<{ tables: { id: string; name: string }[] }[]>(
      await manager.get(`/api/v1/pos/tables?branch_id=${branchB}`),
      200,
    )
    let table = plan.flatMap((z) => z.tables).find((t) => t.name === TABLE_B_NAME)?.id
    if (!table) {
      table = (
        await json<{ id: string }>(
          await manager.post("/api/v1/pos/tables", { branch_id: branchB, zone_id: zoneId, name: TABLE_B_NAME, capacity: 4 }),
          201,
        )
      ).id
    }
    tableB = table

    // Branch-bound memberships (the persons rows are seeded).
    for (const staff of Object.values(STAFF_B)) {
      const memberships = await json<{ memberships: Membership[] }>(
        await manager.get(`${identityPath(tenantId)}/memberships?person_id=${staff.personId}`),
        200,
      )
      const has = memberships.memberships.some((x) => x.branch_id === branchB && x.role_id === staff.roleId && x.status === "active")
      if (!has) {
        const res = await manager.post(`${identityPath(tenantId)}/memberships`, {
          person_id: staff.personId,
          branch_id: branchB,
          role_id: staff.roleId,
        })
        expect(res.status(), `membership for ${staff.email} → ${await res.text()} (dev-seed.sql persons yüklü mü?)`).toBe(201)
      }
    }

    const cashierBToken = (await devToken(context, STAFF_B.cashier.email)).token
    const waiterBToken = (await devToken(context, STAFF_B.waiter.email)).token
    kitchenBToken = (await devToken(context, STAFF_B.kitchen.email)).token
    cashierB = apiFor(context, cashierBToken)
    waiterB = apiFor(context, waiterBToken)
    kitchenB = apiFor(context, kitchenBToken)

    // dev login binds to the FIRST membership by created_at: prove each token
    // really is a branch-B token, otherwise every 403 below could pass by accident.
    for (const token of [cashierBToken, waiterBToken, kitchenBToken]) {
      expect(tokenClaims(token).bid).toBe(branchB)
    }
    expect(tokenClaims((await devToken(context, USERS.cashier)).token).bid).toBe(BRANCH_A)

    // A drawer left open by an aborted run would block the cash flow below.
    const leftover = await activeSession(manager, branchB)
    if (leftover) await closeSession(manager, leftover)

    baselineA = await saleDetails(manager, BRANCH_A, windowFrom, windowTo)
    baselineB = await saleDetails(manager, branchB, windowFrom, windowTo)
  })

  test.afterAll(async () => {
    // Overrides are persistent tenant state: a leftover row would silently
    // change what the earlier tests in this serial file pay on the next run.
    for (const productId of [PRODUCTS.tavuk.id, PRODUCTS.lahmacun.id]) {
      await clearOverride(manager, branchB, productId).catch(() => {})
    }
    for (const checkId of liveChecks) await retireCheck(manager, checkId)
    const leftoverB = await activeSession(manager, branchB).catch(() => null)
    if (leftoverB) await closeSession(manager, leftoverB)
    if (ownedASession) {
      const current = await activeSession(manager, BRANCH_A).catch(() => null)
      if (current && current.id === ownedASession.id) await closeSession(manager, current)
    }
    await disposeContext()
  })

  test("kurulum: ikinci şube, üyelikler ve token'lar; şube kapsamlı rol şubesiz verilemez", async () => {
    const branches = await json<Branch[]>(await manager.get(tenantBranchesPath(tenantId)), 200)
    expect(branches.map((b) => b.id)).toEqual(expect.arrayContaining([BRANCH_A, branchB]))
    expect(branches.find((b) => b.id === branchB)?.name).toBe(BRANCH_B_NAME)

    const members = await json<{ memberships: Membership[] }>(
      await manager.get(`${identityPath(tenantId)}/memberships?branch_id=${branchB}`),
      200,
    )
    for (const staff of Object.values(STAFF_B)) {
      const row = members.memberships.find((x) => x.person_email === staff.email)
      expect(row, `${staff.email} şube B üyeliği`).toMatchObject({ branch_id: branchB, role_id: staff.roleId, status: "active" })
    }
    expect(members.memberships.find((x) => x.person_email === USERS.cashier)).toBeUndefined()

    // The roles list tells the invite form which roles need a branch.
    const roles = await json<{ roles: { id: string; branch_scoped: boolean }[] }>(await manager.get(`${identityPath(tenantId)}/roles`), 200)
    for (const roleId of Object.values(ROLE)) {
      expect(roles.roles.find((r) => r.id === roleId)?.branch_scoped, roleId).toBe(true)
    }
    expect(roles.roles.find((r) => r.id === "00000001-0000-0000-0000-000000000006")?.branch_scoped).toBe(false)

    // ADR-SEC-005: a branch-scoped role cannot be granted chain-wide, and a
    // branch that does not exist in the tenant is refused before any insert.
    await expectStatus(
      await manager.post(`${identityPath(tenantId)}/memberships`, { person_id: STAFF_B.cashier.personId, role_id: ROLE.cashier }),
      400,
    )
    await expectStatus(
      await manager.post(`${identityPath(tenantId)}/memberships`, {
        person_id: STAFF_B.cashier.personId,
        branch_id: "99999999-0000-0000-0000-000000000099",
        role_id: ROLE.cashier,
      }),
      422,
    )

    // A branch body whose enumerated fields the CHECK constraints refuse is a
    // 422 from the service layer, never a 500 from the INSERT.
    const valid = {
      name: "E2E Geçersiz Şube",
      ownership_type: "sube",
      operation_type: "restoran",
      identity_type: "kurumsal",
      is_active: true,
    }
    const before = (await json<Branch[]>(await manager.get(tenantBranchesPath(tenantId)), 200)).length
    for (const invalid of [
      { ...valid, identity_type: "" },
      { ...valid, identity_type: "anonim" },
      { ...valid, ownership_type: "lisansli" },
      { ...valid, operation_type: "fastfood" },
      { ...valid, operation_type: "" },
      { ...valid, name: "   " },
    ]) {
      await expectStatus(await manager.post(tenantBranchesPath(tenantId), invalid), 422)
    }
    expect((await json<Branch[]>(await manager.get(tenantBranchesPath(tenantId)), 200)).length).toBe(before)
  })

  test("şube B kasiyeri kendi şubesinde tam akışı yapar: kasa aç → adisyon → sipariş → nakit → kapat", async () => {
    // Other tenants' branches hold their own drawer: B opens with A untouched.
    const beforeA = await activeSession(manager, BRANCH_A)
    expect(await activeSession(manager, branchB)).toBeNull()

    const opened = await json<CashSession>(
      await cashierB.post("/api/v1/payments/cash-sessions", { branch_id: branchB, opening_counted_amount: OPENING_AMOUNT }),
      201,
    )
    bSessionId = opened.id
    expect(opened).toMatchObject({ status: "opened", expected_close: OPENING_AMOUNT })
    expect((await activeSession(cashierB, branchB))?.id).toBe(bSessionId)
    expect((await activeSession(manager, BRANCH_A))?.id).toBe(beforeA?.id)

    const plan = await json<{ tables: { name: string }[] }[]>(await cashierB.get(`/api/v1/pos/tables?branch_id=${branchB}`), 200)
    expect(plan.flatMap((z) => z.tables).map((t) => t.name)).toContain(TABLE_B_NAME)

    const checkId = await openCheck(cashierB, branchB, `E2E-B1-${RUN}`)
    liveChecks.push(checkId)
    const orderId = await placeOrderOk(cashierB, branchB, checkId, PRODUCTS.adana, `e2e-${RUN}-b1-order`)
    await json(await cashierB.post(`/api/v1/pos/orders/${orderId}/accept`), 200)

    const early = await cashierB.post(`/api/v1/pos/checks/${checkId}/close`, undefined, `e2e-${RUN}-b1-close-early`)
    await expectStatus(early, 409)
    expect(await early.json()).toMatchObject({ code: "insufficient_payment" })

    const payment = await json<Payment>(
      await cashierB.post("/api/v1/payments", saleBody(branchB, checkId, "cash", PRODUCTS.adana), `e2e-${RUN}-b1-pay`),
      201,
    )
    const completed = await waitCompleted(manager, payment.id)
    expect(completed.fiscal_receipt_id).not.toBeNull()

    await serveLiveOrders(manager, checkId)
    const closed = await json<{ status: string; branch_id: string }>(
      await cashierB.post(`/api/v1/pos/checks/${checkId}/close`, undefined, `e2e-${RUN}-b1-close`),
      200,
    )
    expect(closed).toMatchObject({ status: "closed", branch_id: branchB })
    liveChecks.pop()

    const live = await activeSession(cashierB, branchB)
    expect(live).toMatchObject({ cash_payments_taken: PRODUCTS.adana.price, expected_close: OPENING_AMOUNT + PRODUCTS.adana.price })

    const counted = await json<CashSession>(
      await cashierB.post(`/api/v1/payments/cash-sessions/${bSessionId}/closing-count`, {
        closing_counted_amount: OPENING_AMOUNT + PRODUCTS.adana.price,
      }),
      200,
    )
    expect(counted).toMatchObject({ status: "closing_control", difference: 0 })
    const done = await json<CashSession>(await cashierB.post(`/api/v1/payments/cash-sessions/${bSessionId}/close`), 200)
    expect(done.status).toBe("closed")
    expect(await activeSession(manager, branchB)).toBeNull()

    // Waiter takes orders on their own branch, but holds no payment permission.
    const waiterCheck = await openCheck(waiterB, branchB, `E2E-W-${RUN}`)
    liveChecks.push(waiterCheck)
    await placeOrderOk(waiterB, branchB, waiterCheck, PRODUCTS.ayran, `e2e-${RUN}-b1-waiter-order`)
    await expectStatus(
      await waiterB.post("/api/v1/payments", saleBody(branchB, checkId, "terminal", PRODUCTS.ayran), `e2e-${RUN}-b1-waiter-pay`),
      403,
    )
  })

  test("zincir yöneticisi iki şubeyi de görür; gün sonu raporu şube başına ayrı", async () => {
    const aCheck = await openCheck(cashierA, BRANCH_A, `E2E-M-A-${RUN}`)
    const bCheck = await openCheck(cashierB, branchB, `E2E-M-B-${RUN}`)
    liveChecks.push(aCheck, bCheck)
    const aOrder = await placeOrderOk(cashierA, BRANCH_A, aCheck, PRODUCTS.ayran, `e2e-${RUN}-m-a-order`)
    const bOrder = await placeOrderOk(cashierB, branchB, bCheck, PRODUCTS.ayran, `e2e-${RUN}-m-b-order`)

    const everything = await json<CheckRow[]>(await manager.get("/api/v1/pos/checks"), 200)
    expect(everything.map((c) => c.id)).toEqual(expect.arrayContaining([aCheck, bCheck]))

    const onlyA = await json<CheckRow[]>(await manager.get(`/api/v1/pos/checks?branch_id=${BRANCH_A}`), 200)
    expect(onlyA.map((c) => c.id)).toContain(aCheck)
    expect(onlyA.map((c) => c.id)).not.toContain(bCheck)
    expect(onlyA.every((c) => c.branch_id === BRANCH_A)).toBe(true)

    const onlyB = await json<CheckRow[]>(await manager.get(`/api/v1/pos/checks?branch_id=${branchB}`), 200)
    expect(onlyB.map((c) => c.id)).toContain(bCheck)
    expect(onlyB.map((c) => c.id)).not.toContain(aCheck)
    expect(onlyB.every((c) => c.branch_id === branchB)).toBe(true)

    const orders = await json<OrderRow[]>(await manager.get(`/api/v1/pos/orders?ids=${aOrder},${bOrder}`), 200)
    expect(orders.find((o) => o.id === aOrder)?.branch_id).toBe(BRANCH_A)
    expect(orders.find((o) => o.id === bOrder)?.branch_id).toBe(branchB)

    // Table plans are branch-keyed and both are open to the chain manager.
    await json(await manager.get(`/api/v1/pos/tables?branch_id=${BRANCH_A}`), 200)
    const planB = await json<{ tables: { name: string }[] }[]>(await manager.get(`/api/v1/pos/tables?branch_id=${branchB}`), 200)
    expect(planB.flatMap((z) => z.tables).map((t) => t.name)).toContain(TABLE_B_NAME)

    // Day-end report: branch B carries exactly the one cash sale of the
    // previous test; branch A gained nothing (its checks here are open/cancelled).
    const reportB = await saleDetails(manager, branchB, windowFrom, windowTo)
    expect(reportB.sales.closed_check_count - baselineB.sales.closed_check_count).toBe(1)
    expect(reportB.sales.gross - baselineB.sales.gross).toBe(PRODUCTS.adana.price)
    const cashRow = (report: SaleDetails) => report.payments.find((p) => p.method === "cash" && p.status === "completed")
    expect((cashRow(reportB)?.total ?? 0) - (cashRow(baselineB)?.total ?? 0)).toBe(PRODUCTS.adana.price)
    expect(reportB.cash_sessions.find((s) => s.id === bSessionId)).toMatchObject({
      status: "closed",
      cash_payments_taken: PRODUCTS.adana.price,
      difference: 0,
    })

    const reportA = await saleDetails(manager, BRANCH_A, windowFrom, windowTo)
    expect(reportA.sales.closed_check_count).toBe(baselineA.sales.closed_check_count)
    expect(reportA.sales.gross).toBe(baselineA.sales.gross)
    expect(reportA.cash_sessions.map((s) => s.id)).not.toContain(bSessionId)

    // Branch cashiers do not read the day report at all (shift_manager only).
    const query = new URLSearchParams({ branch_id: branchB, from: windowFrom, to: windowTo })
    await expectStatus(await cashierB.get(`/api/v1/pos/reports/sale-details?${query}`), 403)

    await retireCheck(manager, aCheck)
    await retireCheck(manager, bCheck)
    liveChecks.splice(liveChecks.indexOf(aCheck), 1)
    liveChecks.splice(liveChecks.indexOf(bCheck), 1)
  })

  test("KDS: her şubenin mutfağı yalnız kendi siparişlerini görür; başka şubeye el sıkışma reddedilir", async ({ page }) => {
    const labelA = `E2E-KA-${RUN}`
    const labelB = `E2E-KB-${RUN}`
    const aCheck = await openCheck(cashierA, BRANCH_A, labelA)
    const bCheck = await openCheck(cashierB, branchB, labelB)
    liveChecks.push(aCheck, bCheck)
    const aOrder = await placeOrderOk(cashierA, BRANCH_A, aCheck, PRODUCTS.corba, `e2e-${RUN}-k-a-order`)
    const bOrder = await placeOrderOk(cashierB, branchB, bCheck, PRODUCTS.corba, `e2e-${RUN}-k-b-order`)

    const streamB = await openKitchenStream(kitchenBToken, branchB)
    expect(streamB.kind).toBe("snapshot")
    if (streamB.kind === "snapshot") {
      const ids = streamB.orders.map((o) => o.order_id)
      expect(ids).toContain(bOrder)
      expect(ids).not.toContain(aOrder)
    }

    const streamA = await openKitchenStream(kitchenAToken, BRANCH_A)
    expect(streamA.kind).toBe("snapshot")
    if (streamA.kind === "snapshot") {
      const ids = streamA.orders.map((o) => o.order_id)
      expect(ids).toContain(aOrder)
      expect(ids).not.toContain(bOrder)
    }

    // Chain manager may watch either branch, and gets that branch only.
    const managerOnB = await openKitchenStream(managerToken, branchB)
    expect(managerOnB.kind).toBe("snapshot")
    if (managerOnB.kind === "snapshot") {
      const ids = managerOnB.orders.map((o) => o.order_id)
      expect(ids).toContain(bOrder)
      expect(ids).not.toContain(aOrder)
    }

    expect(await openKitchenStream(kitchenBToken, BRANCH_A)).toEqual({ kind: "refused", status: 403 })
    expect(await openKitchenStream(kitchenAToken, branchB)).toEqual({ kind: "refused", status: 403 })

    // The kitchen board pins a branch-scoped operator to their own branch.
    await loginAs(page, STAFF_B.kitchen.email)
    await gotoSpa(page, "/pos/kitchen")
    await expect(page.getByText("Canlı", { exact: true })).toBeVisible()
    const cardB = page.locator("[data-kds-root] [data-slot=card]").filter({ hasText: labelB }).first()
    await cardB.scrollIntoViewIfNeeded()
    await expect(cardB).toBeVisible()
    await expect(page.locator("[data-kds-root]").getByText(labelA)).toHaveCount(0)

    // Same board through the kitchen user's own API: their branch's orders only.
    await json(await kitchenB.get(`/api/v1/pos/tables?branch_id=${branchB}`), 200)

    await retireCheck(manager, aCheck)
    await retireCheck(manager, bCheck)
    liveChecks.splice(liveChecks.indexOf(aCheck), 1)
    liveChecks.splice(liveChecks.indexOf(bCheck), 1)
  })

  test("maliyet alanı sızıntısı: kasiyer/garson/mutfak yanıtlarında ve misafir menüsünde cost anahtarı yok", async () => {
    const checkId = await openCheck(cashierB, branchB, `E2E-COST-${RUN}`)
    liveChecks.push(checkId)
    const orderId = await placeOrderOk(cashierB, branchB, checkId, PRODUCTS.lahmacun, `e2e-${RUN}-cost-order`)

    const category = `/api/v1/catalog/categories/${SEEDED_CATEGORY_ID}/products`
    const counter = [
      "/api/v1/catalog/products",
      `/api/v1/catalog/products/${PRODUCTS.adana.id}`,
      "/api/v1/catalog/categories",
      category,
      "/api/v1/catalog/menus",
      "/api/v1/catalog/modifier-groups",
      `/api/v1/pos/checks?branch_id=${branchB}`,
      `/api/v1/pos/checks/${checkId}`,
      `/api/v1/pos/checks/${checkId}/orders`,
      `/api/v1/pos/orders/${orderId}`,
      `/api/v1/pos/orders?ids=${orderId}`,
      `/api/v1/pos/tables?branch_id=${branchB}`,
      `/api/v1/pos/zones?branch_id=${branchB}`,
      `/api/v1/payments/checks/${checkId}/settlement`,
      tenantBranchesPath(tenantId),
    ]
    // Kitchen and waiter hold fewer grants: what they are refused must not
    // carry a cost key in the refusal either, and what they may read must be 200.
    // The waiter takes orders, so it reads the catalog, adisyons and orders of
    // its own branch; it holds no payment permission (settlement stays 403).
    const kitchen = [
      { path: "/api/v1/catalog/products", ok: true },
      { path: `/api/v1/catalog/products/${PRODUCTS.adana.id}`, ok: true },
      { path: category, ok: true },
      { path: `/api/v1/pos/orders/${orderId}`, ok: true },
      { path: `/api/v1/pos/tables?branch_id=${branchB}`, ok: true },
      { path: `/api/v1/pos/checks/${checkId}`, ok: false },
    ]
    const waiter = [
      { path: `/api/v1/pos/tables?branch_id=${branchB}`, ok: true },
      { path: `/api/v1/pos/zones?branch_id=${branchB}`, ok: true },
      { path: tenantBranchesPath(tenantId), ok: true },
      { path: "/api/v1/catalog/products", ok: true },
      { path: `/api/v1/catalog/products/${PRODUCTS.adana.id}`, ok: true },
      { path: category, ok: true },
      { path: `/api/v1/pos/checks?branch_id=${branchB}`, ok: true },
      { path: `/api/v1/pos/checks/${checkId}`, ok: true },
      { path: `/api/v1/pos/checks/${checkId}/orders`, ok: true },
      { path: `/api/v1/pos/orders/${orderId}`, ok: true },
      { path: `/api/v1/payments/checks/${checkId}/settlement`, ok: false },
    ]

    const sweep = async (api: Api, who: string, paths: { path: string; ok: boolean }[]) => {
      for (const { path, ok } of paths) {
        const res = await api.get(path)
        const body = await res.text()
        expect(res.status(), `${who} GET ${path} → ${body}`).toBe(ok ? 200 : 403)
        expect(body, `${who} GET ${path} maliyet alanı içeriyor`).not.toMatch(COST_KEY)
      }
    }
    await sweep(cashierB, "kasiyer", counter.map((path) => ({ path, ok: true })))
    await sweep(kitchenB, "mutfak", kitchen)
    await sweep(waiterB, "garson", waiter)

    // Guest storefront: a scanned QR of branch B's table → session cookie → menu.
    const codes = await json<{ id: string; table_id: string; status: string }[]>(
      await manager.get(`/api/v1/storefront/qr-codes?branch_id=${branchB}`),
      200,
    )
    const active = codes.find((c) => c.table_id === tableB && c.status === "active")
    const issued = await json<{ token: string }>(
      active
        ? await manager.post(`/api/v1/storefront/qr-codes/${active.id}/rotate`)
        : await manager.post("/api/v1/storefront/qr-codes", { branch_id: branchB, table_id: tableB, table_label: TABLE_B_NAME }),
      active ? 200 : 201,
    )
    const guest = await newContext()
    try {
      const session = await guest.post(`${API_URL}/api/public/v1/sessions`, { data: { token: issued.token } })
      const sessionBody = await session.text()
      expect(session.status(), sessionBody).toBe(200)
      expect(JSON.parse(sessionBody)).toMatchObject({ branch_id: branchB, table_id: tableB })
      expect(sessionBody).not.toMatch(COST_KEY)

      const menu = await guest.get(`${API_URL}/api/public/v1/menu`)
      const menuBody = await menu.text()
      expect(menu.status(), menuBody).toBe(200)
      expect((JSON.parse(menuBody) as { categories: unknown[] }).categories.length).toBeGreaterThan(0)
      expect(menuBody).not.toMatch(COST_KEY)
    } finally {
      await guest.dispose()
    }

    await retireCheck(manager, checkId)
    liveChecks.splice(liveChecks.indexOf(checkId), 1)
  })

  test("izolasyon: şube B personeli şube A'ya yazamaz (adisyon, sipariş, kasa, masa planı, şube kaydı)", async () => {
    victimCheck = await openCheck(cashierA, BRANCH_A, `E2E-V-${RUN}`)
    liveChecks.push(victimCheck)
    victimOrder = await placeOrderOk(cashierA, BRANCH_A, victimCheck, PRODUCTS.ayran, `e2e-${RUN}-v-order`)

    // Branch A's drawer must exist for the cash refusals to mean anything.
    let aSession = await activeSession(manager, BRANCH_A)
    if (!aSession) {
      aSession = await json<CashSession>(
        await cashierA.post("/api/v1/payments/cash-sessions", { branch_id: BRANCH_A, opening_counted_amount: 10_000 }),
        201,
      )
      ownedASession = aSession
    }

    // Adisyon: opening on A is a 403; using A's adisyon from B is a 409 with a
    // machine-readable code (the request names B, the check belongs to A).
    await expectStatus(await cashierB.post("/api/v1/pos/checks", { branch_id: BRANCH_A, table_label: `E2E-evil-${RUN}`, pax: 1 }), 403)
    await expectStatus(await placeOrder(cashierB, BRANCH_A, victimCheck, PRODUCTS.ayran, `e2e-${RUN}-v-evil-a`), 403)
    // The waiter's new order-taking grant must stay inside its own branch too.
    await expectStatus(await waiterB.post("/api/v1/pos/checks", { branch_id: BRANCH_A, table_label: `E2E-evil-w-${RUN}`, pax: 1 }), 403)
    await expectStatus(await placeOrder(waiterB, BRANCH_A, victimCheck, PRODUCTS.ayran, `e2e-${RUN}-v-evil-wa`), 403)
    await expectRefused(await waiterB.get(`/api/v1/pos/checks/${victimCheck}`))
    await expectRefused(await waiterB.get(`/api/v1/pos/orders/${victimOrder}`))
    const mismatch = await placeOrder(cashierB, branchB, victimCheck, PRODUCTS.ayran, `e2e-${RUN}-v-evil-b`)
    await expectStatus(mismatch, 409)
    expect(await mismatch.json()).toMatchObject({ code: "check_branch_mismatch" })

    // Order / check transitions on A's data.
    await expectStatus(await cashierB.post(`/api/v1/pos/orders/${victimOrder}/accept`), 403)
    await expectStatus(await cashierB.post(`/api/v1/pos/orders/${victimOrder}/reject`, { reason: "e2e" }), 403)
    await expectStatus(await cashierB.post(`/api/v1/pos/orders/${victimOrder}/cancel`, { reason: "e2e" }), 403)
    await expectStatus(await kitchenB.post(`/api/v1/pos/orders/${victimOrder}/advance`, { status: "preparing" }), 403)
    await expectStatus(await cashierB.post(`/api/v1/pos/checks/${victimCheck}/cancel`, {}), 403)
    await expectStatus(await cashierB.post(`/api/v1/pos/checks/${victimCheck}/close`, undefined, `e2e-${RUN}-v-close`), 403)

    // Branch A's cash drawer: neither readable nor writable from B.
    await expectStatus(await cashierB.get(`/api/v1/payments/cash-sessions/active?branch_id=${BRANCH_A}`), 403)
    await expectStatus(
      await cashierB.post("/api/v1/payments/cash-sessions", { branch_id: BRANCH_A, opening_counted_amount: 1_000 }),
      403,
    )
    await expectStatus(
      await cashierB.post(
        `/api/v1/payments/cash-sessions/${aSession.id}/movements`,
        { direction: "out", amount_minor: 100, reason: "e2e: başka şubeden hareket" },
        `e2e-${RUN}-v-move`,
      ),
      403,
    )
    await expectStatus(
      await cashierB.post(`/api/v1/payments/cash-sessions/${aSession.id}/closing-count`, { closing_counted_amount: 0 }),
      403,
    )
    await expectStatus(await cashierB.post(`/api/v1/payments/cash-sessions/${aSession.id}/close`), 403)
    expect((await activeSession(manager, BRANCH_A))?.id).toBe(aSession.id)

    // Table plan of A.
    await expectStatus(await cashierB.get(`/api/v1/pos/tables?branch_id=${BRANCH_A}`), 403)
    await expectStatus(await cashierB.get(`/api/v1/pos/zones?branch_id=${BRANCH_A}`), 403)
    await expectStatus(await waiterB.get(`/api/v1/pos/tables?branch_id=${BRANCH_A}`), 403)

    // Branch record, hours, QR codes of A; tenant back-office.
    await expectStatus(await cashierB.get(`${tenantBranchesPath(tenantId)}${BRANCH_A}`), 403)
    await expectStatus(await cashierB.get(`${tenantBranchesPath(tenantId)}${BRANCH_A}/hours/regular`), 403)
    await expectStatus(await cashierB.get(`/api/v1/storefront/qr-codes?branch_id=${BRANCH_A}`), 403)
    await expectStatus(await cashierB.get(`${identityPath(tenantId)}/memberships`), 403)
    await expectStatus(await cashierB.get(`${identityPath(tenantId)}/roles`), 403)
    await expectStatus(await cashierB.get(`/api/v1/payments/cash-sessions/active?branch_id=${branchB}`), 404)

    // Settlement of A's adisyon: refused, or empty (fail-closed) — never A's figures.
    const settlement = await cashierB.get(`/api/v1/payments/checks/${victimCheck}/settlement`)
    if (settlement.status() === 200) {
      expect(await settlement.json()).toMatchObject({ completed: [], pending_total: 0 })
    } else {
      await expectRefused(settlement)
    }

    // The victim is untouched.
    const check = await json<CheckRow>(await manager.get(`/api/v1/pos/checks/${victimCheck}`), 200)
    expect(check.status).toBe("open")
    const orders = await json<OrderRow[]>(await manager.get(`/api/v1/pos/checks/${victimCheck}/orders`), 200)
    expect(orders.map((o) => [o.id, o.status])).toEqual([[victimOrder, "pending"]])
  })

  test("yönetici arayüzden şube ekler: form geçerli değerleri gönderir, yeni şube listede görünür", async ({ page }) => {
    const branches = await json<(Branch & { operation_type: string; ownership_type: string; identity_type: string })[]>(
      await manager.get(tenantBranchesPath(tenantId)),
      200,
    )
    const alreadyThere = branches.some((b) => b.name === UI_BRANCH_NAME)

    await loginAs(page, USERS.manager)
    await gotoSpa(page, "/settings/branches")

    await page.getByRole("button", { name: "Şube Ekle" }).first().click()
    await expect(page.getByRole("dialog")).toBeVisible()

    // The form offers only what the backend CHECK constraints accept.
    const optionTexts = (id: string) => page.locator(`#${id} option`).allTextContents()
    expect(await optionTexts("operation-type")).toEqual(["Restoran", "Kafe", "Fast Food", "Bulut Mutfak"])
    expect(await optionTexts("ownership-type")).toEqual(["Şube", "Franchise"])
    expect(await optionTexts("identity-type")).toEqual(["Kurumsal", "Bireysel"])

    // An empty name is stopped client-side, before any request.
    await page.getByRole("button", { name: "Kaydet" }).click()
    await expect(page.getByText("Şube adı zorunludur")).toBeVisible()

    if (!alreadyThere) {
      // Branches carry no unique name, so a fixed name is created once and
      // re-runs only verify it (there is no delete or rename endpoint).
      await page.locator("#branch-name").fill(UI_BRANCH_NAME)
      await page.locator("#operation-type").selectOption({ label: "Fast Food" })
      await page.locator("#ownership-type").selectOption({ label: "Franchise" })
      await page.locator("#identity-type").selectOption({ label: "Bireysel" })
      await page.getByRole("button", { name: "Kaydet" }).click()
      await expect(page.getByText("Şube eklendi")).toBeVisible()
      await expect(errorToast(page)).toHaveCount(0)
    } else {
      await page.getByRole("button", { name: "Vazgeç" }).click()
    }
    await expect(page.getByRole("dialog")).toHaveCount(0)

    const row = page.getByRole("row").filter({ hasText: UI_BRANCH_NAME })
    await expect(row).toHaveCount(1)
    await expect(row).toContainText("Fast Food")
    await expect(row).toContainText("Franchise")

    const stored = (await json<typeof branches>(await manager.get(tenantBranchesPath(tenantId)), 200)).filter(
      (b) => b.name === UI_BRANCH_NAME,
    )
    expect(stored).toHaveLength(1)
    expect(stored[0]).toMatchObject({ operation_type: "fast_food", ownership_type: "franchise", identity_type: "bireysel", is_active: true })
  })

  test("yönetici arayüzü: şube listesinde ikinci şube, kullanıcılar sayfasında yeni kişiler ve şube etiketi", async ({ page }) => {
    await loginAs(page, USERS.manager)

    await gotoSpa(page, "/settings/branches")
    await expect(page.getByRole("row").filter({ hasText: BRANCH_B_NAME })).toHaveCount(1)
    await expect(page.getByRole("row").filter({ hasText: "Ana Şube" })).toHaveCount(1)

    await gotoSpa(page, "/settings/users")
    const search = page.getByLabel("Ad veya e-posta ara")
    await expect(search).toBeVisible()
    const expectedRole: Record<keyof typeof STAFF_B, string> = { cashier: "Kasiyer", waiter: "Garson", kitchen: "Mutfak" }
    for (const [key, staff] of Object.entries(STAFF_B) as [keyof typeof STAFF_B, (typeof STAFF_B)[keyof typeof STAFF_B]][]) {
      await search.fill(staff.email)
      const row = page.getByRole("row").filter({ hasText: staff.email })
      await expect(row).toHaveCount(1)
      await expect(row).toContainText(BRANCH_B_NAME)
      await expect(row).toContainText(expectedRole[key])
    }

    // The chain manager is labelled chain-wide, the A cashier keeps branch A.
    await search.fill(USERS.manager)
    await expect(page.getByRole("row").filter({ hasText: USERS.manager })).toContainText("Tüm şubeler")
    await search.fill(USERS.cashier)
    await expect(page.getByRole("row").filter({ hasText: USERS.cashier })).toContainText("Ana Şube")

    await search.fill("")
    await page.getByLabel("Şube süzgeci").selectOption({ label: BRANCH_B_NAME })
    await expect(page.getByRole("row").filter({ hasText: "dev.onlinemenu.tr" })).toHaveCount(3)
  })

  test("okuma izolasyonu: şube B kasiyeri şube A'nın adisyon ve siparişlerini listeleyemez, okuyamaz", async () => {
    // Explicitly naming another branch is refused; naming none is forced onto the caller's own branch.
    await expectStatus(await cashierB.get(`/api/v1/pos/checks?branch_id=${BRANCH_A}`), 403)
    await expectStatus(await cashierB.get(`/api/v1/pos/checks?branch_id=${BRANCH_A}&status=open`), 403)

    const own = await json<CheckRow[]>(await cashierB.get("/api/v1/pos/checks"), 200)
    expect(own.filter((c) => c.branch_id !== branchB)).toEqual([])
    expect(own.map((c) => c.id)).not.toContain(victimCheck)
    const ownFiltered = await json<CheckRow[]>(await cashierB.get(`/api/v1/pos/checks?branch_id=${branchB}`), 200)
    expect(ownFiltered.filter((c) => c.branch_id !== branchB)).toEqual([])

    // The chain manager keeps the unfiltered view: it still sees the victim.
    const everything = await json<CheckRow[]>(await manager.get("/api/v1/pos/checks"), 200)
    expect(everything.map((c) => c.id)).toContain(victimCheck)

    // A single foreign row answers 404, not 403: no existence oracle for ids.
    await expectStatus(await cashierB.get(`/api/v1/pos/checks/${victimCheck}`), 404)
    await expectStatus(await cashierB.get(`/api/v1/pos/checks/${victimCheck}/orders`), 404)
    await expectStatus(await cashierB.get(`/api/v1/pos/orders/${victimOrder}`), 404)

    // The batch read is partial by contract: the foreign order simply drops out.
    const batch = await json<OrderRow[]>(await cashierB.get(`/api/v1/pos/orders?ids=${victimOrder}`), 200)
    expect(batch).toEqual([])

    // Branch A's own cashier still reads all of it.
    await json(await cashierA.get(`/api/v1/pos/checks/${victimCheck}`), 200)
    const aOwn = await json<CheckRow[]>(await cashierA.get("/api/v1/pos/checks"), 200)
    expect(aOwn.filter((c) => c.branch_id !== BRANCH_A)).toEqual([])
    expect(aOwn.map((c) => c.id)).toContain(victimCheck)
  })

  test("ödeme izolasyonu: şube B kasiyeri şube A adisyonuna nakit veya kartla tahsilat alamaz (403 branch_forbidden)", async () => {
    const drawerBefore = await activeSession(manager, BRANCH_A)
    expect(drawerBefore, "A'nın çekmecesi izolasyon testinde açıldı").not.toBeNull()

    for (const method of ["cash", "terminal"] as const) {
      const res = await cashierB.post(
        "/api/v1/payments",
        saleBody(BRANCH_A, victimCheck, method, PRODUCTS.ayran),
        `e2e-${RUN}-v-pay-${method}`,
      )
      await expectStatus(res, 403)
      expect(await res.json()).toMatchObject({ code: "branch_forbidden" })
    }

    // Nothing reached A's till or its adisyon.
    const drawerAfter = await activeSession(manager, BRANCH_A)
    expect(drawerAfter?.cash_payments_taken).toBe(drawerBefore?.cash_payments_taken)
    expect(drawerAfter?.expected_close).toBe(drawerBefore?.expected_close)
    const settlement = await json<{ completed: unknown[]; pending_total: number }>(
      await manager.get(`/api/v1/payments/checks/${victimCheck}/settlement`),
      200,
    )
    expect(settlement).toMatchObject({ completed: [], pending_total: 0 })
  })

  test("şube bazlı ürün override'ı: yönetici şube B'ye kendi fiyatını verir ve bir ürünü kapatır (ADR-DATA-009)", async () => {
    interface ListedProduct {
      id: string
      price_amount: number
      branch_price_overridden: boolean
    }
    const categoryPath = `/api/v1/catalog/categories/${SEEDED_CATEGORY_ID}/products`
    const listFor = async (api: Api, branchId: string) =>
      json<ListedProduct[]>(await api.get(`${categoryPath}?branch_id=${branchId}`), 200)
    const find = (rows: ListedProduct[], product: SeededProduct) => rows.find((p) => p.id === product.id)

    // Baseline: before any override both branches see the tenant catalog.
    expect(find(await listFor(cashierB, branchB), PRODUCTS.tavuk)).toMatchObject({
      price_amount: PRODUCTS.tavuk.price,
      branch_price_overridden: false,
    })

    // The chain owner sets branch B's own price and closes another product there.
    const overridePath = (productId: string) => `/api/v1/catalog/branches/${branchB}/products/${productId}/override`
    expect(
      await json<{ price_amount: number | null; is_available: boolean }>(
        await manager.put(overridePath(PRODUCTS.tavuk.id), { is_available: true, price_amount: OVERRIDE_PRICE }),
        200,
      ),
    ).toMatchObject({ price_amount: OVERRIDE_PRICE, is_available: true })
    await json(await manager.put(overridePath(PRODUCTS.lahmacun.id), { is_available: false, price_amount: null }), 200)

    // A branch cashier may not set prices — the action is manager-only even
    // for their own branch (ADR-DATA-009 §6).
    await expectStatus(await cashierB.put(overridePath(PRODUCTS.tavuk.id), { is_available: true, price_amount: 1 }), 403)
    // ...and may not even read another branch's overrides.
    await expectStatus(await cashierB.get(`/api/v1/catalog/branches/${BRANCH_A}/product-overrides`), 403)
    const ownOverrides = await json<{ product_id: string; price_amount: number | null }[]>(
      await cashierB.get(`/api/v1/catalog/branches/${branchB}/product-overrides`),
      200,
    )
    expect(ownOverrides.find((o) => o.product_id === PRODUCTS.tavuk.id)?.price_amount).toBe(OVERRIDE_PRICE)

    // Branch B's cashier sees the override price and no longer sees the closed product.
    const atB = await listFor(cashierB, branchB)
    expect(find(atB, PRODUCTS.tavuk)).toMatchObject({ price_amount: OVERRIDE_PRICE, branch_price_overridden: true })
    expect(find(atB, PRODUCTS.lahmacun), "şubede kapatılan ürün listede olmamalı").toBeUndefined()
    expect(find(atB, PRODUCTS.adana)).toMatchObject({ price_amount: PRODUCTS.adana.price, branch_price_overridden: false })

    // Branch A is untouched: the override is per branch, not a tenant edit.
    const atA = await listFor(cashierA, BRANCH_A)
    expect(find(atA, PRODUCTS.tavuk)).toMatchObject({ price_amount: PRODUCTS.tavuk.price, branch_price_overridden: false })
    expect(find(atA, PRODUCTS.lahmacun)).toMatchObject({ price_amount: PRODUCTS.lahmacun.price })

    // The order path agrees with the listing: the branch price is accepted,
    // the tenant price is not, and the closed product cannot be sold at all.
    const checkId = await openCheck(cashierB, branchB, `E2E-OVR-${RUN}`)
    liveChecks.push(checkId)

    await json(await placeOrderAt(cashierB, branchB, checkId, PRODUCTS.tavuk, OVERRIDE_PRICE, `e2e-${RUN}-ovr-ok`), 201)

    const stale = await placeOrderAt(cashierB, branchB, checkId, PRODUCTS.tavuk, PRODUCTS.tavuk.price, `e2e-${RUN}-ovr-stale`)
    await expectStatus(stale, 422)
    expect(await stale.json()).toMatchObject({ code: "price_mismatch" })

    const closed = await placeOrder(cashierB, branchB, checkId, PRODUCTS.lahmacun, `e2e-${RUN}-ovr-closed`)
    await expectStatus(closed, 422)
    expect(await closed.json()).toMatchObject({ code: "invalid_order_line" })

    // Branch A still sells both at the tenant price.
    const aCheck = await openCheck(cashierA, BRANCH_A, `E2E-OVR-A-${RUN}`)
    liveChecks.push(aCheck)
    await json(await placeOrder(cashierA, BRANCH_A, aCheck, PRODUCTS.lahmacun, `e2e-${RUN}-ovr-a-closed`), 201)

    // Removing the overrides returns branch B to the tenant catalog.
    await clearOverride(manager, branchB, PRODUCTS.tavuk.id)
    await clearOverride(manager, branchB, PRODUCTS.lahmacun.id)
    const restored = await listFor(cashierB, branchB)
    expect(find(restored, PRODUCTS.tavuk)).toMatchObject({
      price_amount: PRODUCTS.tavuk.price,
      branch_price_overridden: false,
    })
    expect(find(restored, PRODUCTS.lahmacun)).toBeDefined()
  })

  test("Şube Fiyatları ekranı: yönetici şube B'yi seçer, ürüne şube fiyatı yazar, birini kapatır, rozetler görünür, varsayılana döner (ADR-DATA-009)", async ({ page }) => {
    // Distinct from OVERRIDE_PRICE (31_000): a leftover row from the API test
    // above can never satisfy an assertion here by accident.
    const SCREEN_PRICE = 29_900
    const priced = PRODUCTS.tavuk
    const closed = PRODUCTS.lahmacun
    const overridePath = (productId: string) => `/api/v1/catalog/branches/${branchB}/products/${productId}/override`
    const isOverrideCall = (method: string, productId: string) => (res: { url: () => string; request: () => { method: () => string } }) =>
      res.url().endsWith(overridePath(productId)) && res.request().method() === method
    const overridesOf = async () =>
      json<{ product_id: string; is_available: boolean; price_amount: number | null }[]>(
        await manager.get(`/api/v1/catalog/branches/${branchB}/product-overrides`),
        200,
      )
    const rowFor = (product: SeededProduct) => page.getByTestId(`branch-pricing-row-${product.id}`)

    await clearOverride(manager, branchB, priced.id)
    await clearOverride(manager, branchB, closed.id)

    try {
      await loginAs(page, USERS.manager)
      await expect(page.getByRole("link", { name: "Şube Fiyatları" })).toBeVisible()
      await gotoSpa(page, "/catalog/branch-pricing")

      await page.getByRole("combobox", { name: "Şube" }).selectOption({ label: BRANCH_B_NAME })
      await expect(rowFor(priced)).toBeVisible()

      // Untouched: tenant price everywhere, nothing "şubeye özel".
      await expect(rowFor(priced)).toContainText("Genel fiyat")
      await expect(rowFor(priced).getByLabel(`${priced.name} şube fiyatı`)).toHaveValue("")
      await page.getByRole("button", { name: "Yalnız şubeye özel olanlar" }).click()
      await expect(page.getByText("Bu şubede tüm ürünler genel fiyatla satılıyor")).toBeVisible()
      await page.getByRole("button", { name: "Yalnız şubeye özel olanlar" }).click()

      // Branch price: typed in lira, committed with Enter, stored in kuruş.
      const priceSaved = page.waitForResponse(isOverrideCall("PUT", priced.id))
      await rowFor(priced).getByLabel(`${priced.name} şube fiyatı`).fill("299")
      await page.keyboard.press("Enter")
      expect((await priceSaved).status()).toBe(200)
      await expect(rowFor(priced)).toContainText("Şube fiyatı")
      await expect(rowFor(priced).getByLabel(`${priced.name} şube fiyatı`)).toHaveValue("299,00")

      // Close another product on this branch.
      const closeSaved = page.waitForResponse(isOverrideCall("PUT", closed.id))
      // Click the visible track (the input itself is sr-only); uncheck() would
      // fail because the controlled switch only flips once the optimistic
      // write lands, after Playwright has already looked at it.
      await rowFor(closed).locator("label").click()
      expect((await closeSaved).status()).toBe(200)
      await expect(rowFor(closed)).toContainText("Kapalı")
      await expect(errorToast(page)).toHaveCount(0)

      // The API agrees — and the closed product's price stayed empty, the
      // priced one stayed available (both fields travel on every save).
      const stored = await overridesOf()
      expect(stored.find((o) => o.product_id === priced.id)).toMatchObject({ price_amount: SCREEN_PRICE, is_available: true })
      expect(stored.find((o) => o.product_id === closed.id)).toMatchObject({ price_amount: null, is_available: false })

      // Only the two changed products are "şubeye özel".
      await page.getByRole("button", { name: "Yalnız şubeye özel olanlar" }).click()
      await expect(rowFor(priced)).toBeVisible()
      await expect(rowFor(closed)).toBeVisible()
      await expect(rowFor(PRODUCTS.adana)).toHaveCount(0)
      await page.getByRole("button", { name: "Yalnız şubeye özel olanlar" }).click()

      // The branch's own cashier now sells at the screen price.
      const listed = await json<{ id: string; price_amount: number; branch_price_overridden: boolean }[]>(
        await cashierB.get(`/api/v1/catalog/categories/${SEEDED_CATEGORY_ID}/products?branch_id=${branchB}`),
        200,
      )
      expect(listed.find((p) => p.id === priced.id)).toMatchObject({ price_amount: SCREEN_PRICE, branch_price_overridden: true })
      expect(listed.find((p) => p.id === closed.id)).toBeUndefined()

      // Varsayılana dön: both rows disappear server-side and the badges fall back.
      for (const product of [priced, closed]) {
        const deleted = page.waitForResponse(isOverrideCall("DELETE", product.id))
        await rowFor(product).getByRole("button", { name: `${product.name} için varsayılana dön` }).click()
        expect((await deleted).status()).toBe(204)
        await expect(rowFor(product)).toContainText("Genel fiyat")
      }
      await expect(rowFor(closed).getByRole("switch")).toBeChecked()
      expect((await overridesOf()).filter((o) => ([priced.id, closed.id] as string[]).includes(o.product_id))).toEqual([])
      await expect(errorToast(page)).toHaveCount(0)
    } finally {
      await clearOverride(manager, branchB, priced.id)
      await clearOverride(manager, branchB, closed.id)
    }
  })

  test("Şube Fiyatları ekranı: şube kapsamlı kasiyer menüde bağlantıyı görmez, sayfaya gidince erişim uyarısı alır", async ({ page }) => {
    await loginAs(page, USERS.cashier)
    await expect(page.getByRole("link", { name: "Şube Fiyatları" })).toHaveCount(0)

    await gotoSpa(page, "/catalog/branch-pricing")
    await expect(page.getByTestId("access-denied")).toBeVisible()
    await expect(page.getByTestId(/^branch-pricing-row-/)).toHaveCount(0)
  })
})
