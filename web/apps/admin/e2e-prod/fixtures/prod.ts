// Production acceptance fixtures.
//
// This directory talks to the LIVE pilot stack (api/pos/menu/auth
// .diverstreetfood.com). Nothing here runs unless E2E_PROD=1 is set — the
// guard below throws at import time, and e2e-prod/playwright.config.ts repeats
// it in globalSetup so a spec that forgets to import this file is still
// refused. Credentials are never committed: they come from the environment,
// normally by sourcing deploy/.env.diverserver.local (git-ignored).
//
// The dev suite under e2e/ is untouched and keeps using /dev/login; production
// has no dev login, so every principal here is minted through Keycloak's
// direct grant on the confidential `e2e-prod` client and exchanged for a CTX
// token (GET /v1/identity/me/contexts -> POST /v1/identity/auth/context).

import { randomUUID } from "node:crypto"

import { type APIRequestContext, type APIResponse, type Page, expect } from "@playwright/test"

export function assertProdOptIn(): void {
  if (process.env.E2E_PROD !== "1") {
    throw new Error(
      "e2e-prod: bu paket canlı prod ortamına yazar. Çalıştırmak için E2E_PROD=1 verin " +
        "(kimlik bilgileri: `set -a; . deploy/.env.diverserver.local; set +a`).",
    )
  }
}

assertProdOptIn()

function env(name: string, fallback?: string): string {
  const value = process.env[name] ?? fallback
  if (!value) throw new Error(`e2e-prod: ${name} ortam değişkeni gerekli`)
  return value
}

export const API_URL = env("E2E_PROD_API_URL", "https://api.diverstreetfood.com")
export const ADMIN_URL = env("E2E_PROD_BASE_URL", "https://pos.diverstreetfood.com")
export const MENU_URL = env("E2E_PROD_MENU_URL", "https://menu.diverstreetfood.com")
export const KEYCLOAK_URL = env("E2E_PROD_KEYCLOAK_URL", "https://auth.diverstreetfood.com")
export const REALM = env("E2E_PROD_REALM", "onlinemenu")

// Every test artefact carries this prefix so a half-finished run is findable
// and distinguishable from the real b2b data that shares this tenant.
export const TAG = "PRODTEST"
export const RUN = `${TAG}-${Date.now().toString(36)}`

export interface Account {
  email: string
  password: string
}

/** Reads one account from the env pair <KEY>_EMAIL / <KEY>_PASSWORD. */
export function account(key: string): Account {
  return { email: env(`${key}_EMAIL`), password: env(`${key}_PASSWORD`) }
}

export const ACCOUNTS = {
  manager: () => account("TEST_YONETICI"),
  cashierSerdivan: () => account("KASIYER_SERDIVAN"),
  waiterSerdivan: () => account("GARSON_SERDIVAN"),
  kitchenSerdivan: () => account("MUTFAK_SERDIVAN"),
  cashierIzmit: () => account("KASIYER_IZMIT"),
  waiterIzmit: () => account("GARSON_IZMIT"),
  kitchenIzmit: () => account("MUTFAK_IZMIT"),
  cashierAdapazari: () => account("KASIYER_ADAPAZARI"),
  cashierKirkpinar: () => account("KASIYER_KIRKPINAR"),
} as const

export async function expectStatus(res: APIResponse, status: number): Promise<void> {
  expect(res.status(), `${res.url()} → ${await res.text()}`).toBe(status)
}

export async function json<T>(res: APIResponse, status: number): Promise<T> {
  await expectStatus(res, status)
  return (await res.json()) as T
}

/** Keycloak access token via the direct grant on the confidential e2e client. */
export async function keycloakToken(request: APIRequestContext, acct: Account): Promise<string> {
  const res = await request.post(`${KEYCLOAK_URL}/realms/${REALM}/protocol/openid-connect/token`, {
    form: {
      grant_type: "password",
      client_id: env("E2E_PROD_CLIENT_ID", "e2e-prod"),
      client_secret: env("E2E_PROD_CLIENT_SECRET"),
      username: acct.email,
      password: acct.password,
    },
  })
  const body = await json<{ access_token: string }>(res, 200)
  return body.access_token
}

export interface Context {
  membership_id: string
  tenant_id: string
  branch_id?: string
  role_id: string
  role_name: string
}

export interface Principal {
  ctx: Context
  api: Api
}

export type Api = ReturnType<typeof apiFor>

/**
 * The production reverse proxy rate-limits the API per client IP
 * (`limit_req zone=api burst=40 nodelay`, 10 r/s — deploy/nginx/diverserver.conf)
 * and answers a throttled request with **503**, not 429. A test run is a burst
 * by nature, so every call retries a 503 a few times before giving up;
 * otherwise a passing scenario would fail on proxy backpressure rather than on
 * application behaviour. Nothing else is retried — a 4xx is an answer.
 */
export async function withRateLimitRetry(send: () => Promise<APIResponse>): Promise<APIResponse> {
  let res = await send()
  for (let attempt = 0; attempt < 4 && res.status() === 503; attempt++) {
    await new Promise((resolve) => setTimeout(resolve, 1_500 * (attempt + 1)))
    res = await send()
  }
  return res
}

export function apiFor(request: APIRequestContext, token: string) {
  const headers = { Authorization: `Bearer ${token}` }
  const withKey = (key?: string) => (key ? { ...headers, "Idempotency-Key": key } : headers)
  return {
    token,
    get: (path: string) => withRateLimitRetry(() => request.get(`${API_URL}${path}`, { headers })),
    // A retried POST is safe here: every mutating route in this suite is
    // either idempotent by key (ADR-SEC-003) or never reached the application
    // at all, because the proxy rejected it before proxy_pass.
    post: (path: string, data?: unknown, key?: string) =>
      withRateLimitRetry(() => request.post(`${API_URL}${path}`, { headers: withKey(key), data })),
    // Most POSTs here are ADR-SEC-003 idempotent routes; a fresh key per call
    // is the "new operation" case, an explicit key the "retry" case. The key is
    // minted once so a rate-limit retry replays instead of duplicating.
    postNew: (path: string, data?: unknown) => {
      const key = `${RUN}-${randomUUID()}`
      return withRateLimitRetry(() => request.post(`${API_URL}${path}`, { headers: withKey(key), data }))
    },
    patch: (path: string, data?: unknown) =>
      withRateLimitRetry(() => request.patch(`${API_URL}${path}`, { headers, data })),
    put: (path: string, data?: unknown) =>
      withRateLimitRetry(() => request.put(`${API_URL}${path}`, { headers, data })),
    del: (path: string) => withRateLimitRetry(() => request.delete(`${API_URL}${path}`, { headers })),
  }
}

/** Keycloak token -> membership list -> CTX token, the production login chain. */
export async function principal(request: APIRequestContext, acct: Account): Promise<Principal> {
  const access = await keycloakToken(request, acct)
  const auth = { Authorization: `Bearer ${access}` }
  const contexts = await json<{ contexts: Context[] }>(
    await withRateLimitRetry(() => request.get(`${API_URL}/v1/identity/me/contexts`, { headers: auth })),
    200,
  )
  expect(contexts.contexts.length, `${acct.email} için üyelik bulunamadı`).toBeGreaterThan(0)
  const ctx = contexts.contexts[0]
  const { token } = await json<{ token: string }>(
    await withRateLimitRetry(() =>
      request.post(`${API_URL}/v1/identity/auth/context`, {
        headers: auth,
        data: { membership_id: ctx.membership_id },
      }),
    ),
    200,
  )
  return { ctx, api: apiFor(request, token) }
}

// ---------------------------------------------------------------------------
// Live tenant lookups — nothing about the pilot data is hardcoded here, so the
// suite keeps working if a branch or product id changes.
// ---------------------------------------------------------------------------

export interface Branch {
  id: string
  name: string
  slug: string
}

export async function branchesBySlug(api: Api, tenantId: string): Promise<Record<string, Branch>> {
  const list = await json<Branch[]>(await api.get(`/tenants/${tenantId}/branches/`), 200)
  return Object.fromEntries(list.map((b) => [b.slug, b]))
}

export interface Product {
  id: string
  name: string
  price_amount: number
  tax_rate_bps: number
  currency: string
  branch_price_overridden: boolean
}

/** Branch-resolved catalog listing (ADR-DATA-009: branch_id is what selects the override). */
export async function products(api: Api, branchId?: string): Promise<Product[]> {
  const query = branchId ? `?branch_id=${branchId}` : ""
  return json<Product[]>(await api.get(`/api/v1/catalog/products${query}`), 200)
}

export async function productByName(api: Api, name: string, branchId?: string): Promise<Product> {
  const found = (await products(api, branchId)).find((p) => p.name === name)
  expect(found, `ürün bulunamadı: ${name}`).toBeTruthy()
  return found as Product
}

export interface PosTable {
  id: string
  name: string
  status: string
}

export interface ZonePlan {
  id: string
  name: string
  tables: PosTable[]
}

export async function tablePlan(api: Api, branchId: string): Promise<ZonePlan[]> {
  return json<ZonePlan[]>(await api.get(`/api/v1/pos/tables?branch_id=${branchId}`), 200)
}

export async function allTables(api: Api, branchId: string): Promise<PosTable[]> {
  return (await tablePlan(api, branchId)).flatMap((z) => z.tables)
}

// ---------------------------------------------------------------------------
// POS flow helpers
// ---------------------------------------------------------------------------

export interface Check {
  id: string
  status: string
  total: number
  table_id: string | null
  table_label: string
  merged_into_check_id?: string
}

export interface OrderItem {
  id: string
  product_name: string
  unit_price_amount: number
  quantity: number
  modifier_ids: string[]
}

export interface Order {
  id: string
  status: string
  check_id: string | null
  items: OrderItem[]
}

export async function openCheck(api: Api, branchId: string, label: string, tableId?: string): Promise<Check> {
  const data: Record<string, unknown> = { branch_id: branchId, pax: 2 }
  if (tableId) data.table_id = tableId
  else data.table_label = label
  return json<Check>(await api.post("/api/v1/pos/checks", data), 201)
}

export function line(product: Product, quantity = 1, price?: number, modifierIds?: string[]) {
  const unit = price ?? product.price_amount
  return {
    product_id: product.id,
    product_name: product.name,
    product_price_amount: unit,
    product_currency: product.currency || "TRY",
    tax_rate_bps: product.tax_rate_bps,
    quantity,
    unit_price_amount: unit,
    ...(modifierIds ? { modifier_ids: modifierIds } : {}),
  }
}

export function placeOrder(api: Api, branchId: string, checkId: string, items: unknown[], key?: string) {
  const path = "/api/v1/pos/orders"
  const data = { branch_id: branchId, check_id: checkId, order_channel: "dine_in", items }
  return key ? api.post(path, data, key) : api.postNew(path, data)
}

export async function placeOrderOk(api: Api, branchId: string, checkId: string, items: unknown[]): Promise<Order> {
  return json<Order>(await placeOrder(api, branchId, checkId, items), 201)
}

const KITCHEN_CHAIN = ["accepted", "preparing", "ready", "delivered"] as const

/**
 * Walks every live order under a check to "delivered".
 *
 * Order matters and is not cosmetic: a closed adisyon refuses order mutations
 * (409 check_not_open), so anything still live at close time can never be
 * cleaned up through the API and stays on the shared kitchen board. Always
 * serve BEFORE closing.
 */
export async function serveLiveOrders(counter: Api, kitchen: Api, checkId: string): Promise<void> {
  const orders = await json<Order[]>(await counter.get(`/api/v1/pos/checks/${checkId}/orders`), 200)
  for (const order of orders) {
    if (order.status === "rejected" || order.status === "cancelled" || order.status === "delivered") continue
    let at = KITCHEN_CHAIN.indexOf(order.status as (typeof KITCHEN_CHAIN)[number])
    if (order.status === "pending") {
      await counter.post(`/api/v1/pos/orders/${order.id}/accept`)
      at = 0
    }
    if (at < 0) continue
    for (const status of KITCHEN_CHAIN.slice(at + 1)) {
      await kitchen.post(`/api/v1/pos/orders/${order.id}/advance`, { status })
    }
  }
}

export interface Payment {
  id: string
  status: string
  method: string
  amount_total: number
  check_id: string | null
  fiscal_receipt_id: string | null
}

/**
 * A cash sale. `lines` is populated on purpose: the fiscal adapter registers
 * the sale line by line (ADR-FISCAL-001) and an empty basket would be a
 * different, weaker assertion than what the counter actually sends.
 */
export function cashSale(branchId: string, checkId: string, name: string, amount: number, quantity = 1) {
  return {
    branch_id: branchId,
    check_id: checkId,
    method: "cash",
    amount_total: amount,
    currency: "TRY",
    lines: [
      {
        name,
        unit_price_minor: Math.round(amount / quantity),
        quantity_milli: quantity * 1000,
        tax_rate_permyriad: 1000,
        unit: "C62",
      },
    ],
  }
}

export async function payCash(
  api: Api,
  branchId: string,
  checkId: string,
  name: string,
  amount: number,
  key?: string,
): Promise<Payment> {
  const data = cashSale(branchId, checkId, name, amount)
  const res = key ? await api.post("/api/v1/payments", data, key) : await api.postNew("/api/v1/payments", data)
  return json<Payment>(res, 201)
}

/** Mock fiscal answers in ~1s; payment.payment.read is manager/shift only. */
export async function waitCompleted(reader: Api, paymentId: string): Promise<Payment> {
  await expect
    .poll(async () => (await json<Payment>(await reader.get(`/api/v1/payments/${paymentId}`), 200)).status, {
      timeout: 30_000,
      intervals: [500],
    })
    .toBe("completed")
  return json<Payment>(await reader.get(`/api/v1/payments/${paymentId}`), 200)
}

export interface CashSession {
  id: string
  status: string
  opening_counted_amount: number
  closing_counted_amount: number | null
  movements_net: number
  cash_payments_taken: number
  expected_close: number
  difference: number | null
}

export async function activeSession(api: Api, branchId: string): Promise<CashSession | null> {
  const res = await api.get(`/api/v1/payments/cash-sessions/active?branch_id=${branchId}`)
  if (res.status() === 404) return null
  return json<CashSession>(res, 200)
}

export interface SaleDetails {
  sales: { closed_check_count: number; gross: number }
  payments: { method: string; status: string; count: number; total: number }[]
  cash_sessions: CashSession[]
}

export async function saleDetails(api: Api, branchId: string, from: string, to: string): Promise<SaleDetails> {
  const query = new URLSearchParams({ branch_id: branchId, from, to })
  return json<SaleDetails>(await api.get(`/api/v1/pos/reports/sale-details?${query}`), 200)
}

/**
 * Best-effort teardown in the only order production accepts:
 * serve live orders -> close/cancel the check -> hand the table back.
 * Assertions in the test body are what fail a run; this only keeps the shared
 * pilot tenant usable for the next one.
 */
export async function cleanupCheck(counter: Api, kitchen: Api, checkId: string): Promise<void> {
  try {
    await serveLiveOrders(counter, kitchen, checkId)
  } catch {
    // ignored — teardown must not mask the real failure
  }
  await counter.post(`/api/v1/pos/checks/${checkId}/cancel`, { reason: `${TAG} temizlik` })
}

/**
 * Hands a table back as empty and confirms it on the floor plan.
 *
 * Closing or cancelling a check moves the table to `cleaning` through the POS
 * event path, which can land a moment AFTER the teardown write — a single
 * status POST then loses the race and leaves the plan dirty (observed on the
 * 2026-09-20 run). Retry until the plan actually reads empty.
 */
export async function releaseTable(manager: Api, branchId: string, tableId: string): Promise<void> {
  for (let attempt = 0; attempt < 5; attempt++) {
    await manager.post(`/api/v1/pos/tables/${tableId}/status`, { status: "empty" })
    const table = (await allTables(manager, branchId)).find((t) => t.id === tableId)
    if (table?.status === "empty") return
    await new Promise((resolve) => setTimeout(resolve, 1_500))
  }
}

// ---------------------------------------------------------------------------
// Admin UI (Keycloak SSO) — production has no /dev/login form.
// ---------------------------------------------------------------------------

/**
 * Signs in to the admin panel through the real Keycloak login page and waits
 * for the app shell. The panel keeps its CTX token in memory only
 * (lib/session-restore.ts), so specs must navigate with the SPA router, never
 * a hard page load.
 */
export async function loginAdmin(page: Page, acct: Account): Promise<void> {
  await page.goto(`${ADMIN_URL}/login`)

  // /login PKCE akışını kendiliğinden başlatmaz — "Keycloak ile giriş"
  // düğmesine basılması gerekir (app/(auth)/login/page.tsx). Düğme, Keycloak
  // parola formu ve uygulama kabuğu (zaten açık bir SSO oturumu varsa) birlikte
  // yarıştırılır; yalnız birini beklemek koşuyu kilitler.
  const shell = page.locator('[data-sidebar="group-label"]').first()
  const username = page.locator("#username")
  const startButton = page.getByRole("button", { name: "Keycloak ile giriş" })

  await expect(startButton.or(username).or(shell)).toBeVisible({ timeout: 45_000 })
  if (await startButton.isVisible().catch(() => false)) {
    await startButton.click()
    await expect(username.or(shell)).toBeVisible({ timeout: 45_000 })
  }

  if (await username.isVisible().catch(() => false)) {
    await username.fill(acct.email)
    await page.locator("#password").fill(acct.password)
    await page.locator("#kc-login").click()
    await page.waitForURL(
      (url) => url.origin === new URL(ADMIN_URL).origin && !url.pathname.startsWith("/login"),
      { timeout: 45_000 },
    )
  }

  await expect(shell).toBeVisible({ timeout: 45_000 })
}

/** In-app navigation: a hard load would drop the in-memory CTX token. */
export async function gotoSpa(page: Page, path: string): Promise<void> {
  const hasRouter = await page.evaluate(() => Boolean((window as unknown as { next?: { router?: unknown } }).next?.router))
  expect(hasRouter, "window.next.router yok — Next.js iç API'si değişmiş olabilir").toBeTruthy()
  await page.evaluate((p) => {
    ;(window as unknown as { next: { router: { push: (p: string) => void } } }).next.router.push(p)
  }, path)
  await page.waitForURL((url) => url.pathname === path)
}
