// Component test for DashboardClient (real gün sonu satış özeti, not the
// hardcoded mockSalesData it replaces). Follows qr-code-dialog.test.tsx's
// wrapper pattern (NextIntl + QueryClient + a full lib/api mock, since
// use-can.ts's cosmetic gate reads the CTX token through the SAME module
// auth-store.setSession writes into) and use-order-details.test.tsx's
// route-dispatching mock (several endpoints are hit from one page).
//
// recharts' ResponsiveContainer measures its parent via ResizeObserver and
// renders nothing in jsdom (zero size), so assertions target the cards and
// tables around the chart, never the chart's own SVG output.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import DashboardClient from "@/app/(main)/dashboard-client"
import messages from "@/messages/tr.json"
import { useAuthStore } from "@/store/auth-store"
import type { SaleDetails } from "@/types"

const { getToken, setToken } = vi.hoisted(() => {
  let token: string | null = null
  return {
    getToken: () => token,
    setToken: (t: string | null) => {
      token = t
    },
  }
})

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal("ResizeObserver", ResizeObserverStub)

const get = vi.fn()

vi.mock("@/lib/api", () => ({
  default: {
    get: (...args: unknown[]) => get(...args),
    post: vi.fn(),
  },
  getAccessToken: () => getToken(),
  setAccessToken: (t: string | null) => setToken(t),
  clearAccessToken: () => setToken(null),
}))

function base64UrlEncode(obj: unknown): string {
  return Buffer.from(JSON.stringify(obj)).toString("base64url")
}

function ctxToken(roleIds: string[]): string {
  const header = base64UrlEncode({ alg: "HS256", typ: "CTX" })
  const payload = base64UrlEncode({ rids: roleIds })
  return `${header}.${payload}.fake-signature`
}

const SHIFT_MANAGER_ID = "00000001-0000-0000-0000-000000000002"
const CASHIER_ID = "00000001-0000-0000-0000-000000000001"
const TENANT_ID = "tenant-1"
const BRANCH_ID = "branch-1"

const SALE_DETAILS: SaleDetails = {
  branch_id: BRANCH_ID,
  from: "2026-09-05T00:00:00.000Z",
  to: "2026-09-06T00:00:00.000Z",
  tz: "Europe/Istanbul",
  sales: { closed_check_count: 2, gross: 28000, item_count: 4, average_check: 14000 },
  cancellations: { check_count: 1, amount: 4000 },
  by_tax_rate: [{ rate_bps: 1000, gross: 23000, base: 20909, tax: 2091 }],
  by_day: [{ date: "2026-09-04", gross: 25000, check_count: 1 }],
  by_source: [{ source: "pos", check_count: 1, gross: 25000 }],
  payments: [{ method: "cash", status: "completed", count: 2, total: 3500 }],
  cash_sessions: [
    {
      id: "session-1",
      status: "closed",
      opened_at: "2026-09-05T06:00:00Z",
      closed_at: "2026-09-05T14:00:00Z",
      opening_counted_amount: 50000,
      cash_payments_taken: 3500,
      movements_net: -1000,
      expected_close: 52500,
      closing_counted_amount: 52500,
      difference: 0,
    },
  ],
}

function mockRoutes() {
  get.mockImplementation((url: string) => {
    if (url === `/tenants/${TENANT_ID}/branches/`) {
      return Promise.resolve({ data: [{ id: BRANCH_ID, name: "Merkez Şube" }] })
    }
    if (url === "/api/v1/pos/reports/sale-details") {
      return Promise.resolve({ data: SALE_DETAILS })
    }
    if (url === "/api/v1/pos/checks") {
      return Promise.resolve({ data: [] })
    }
    if (url === "/api/v1/catalog/products") {
      return Promise.resolve({ data: [] })
    }
    return Promise.resolve({ data: [] })
  })
}

function Wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <NextIntlClientProvider locale="tr" messages={messages}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </NextIntlClientProvider>
  )
}

function loginAs(roleIds: string[]) {
  useAuthStore
    .getState()
    .setSession(ctxToken(roleIds), { id: "person-1", name: "Test", email: "test@onlinemenu.tr" }, TENANT_ID)
}

describe("DashboardClient", () => {
  beforeEach(() => {
    get.mockReset()
    mockRoutes()
  })

  afterEach(() => {
    useAuthStore.getState().logout()
  })

  it("renders the real sale summary for a shift_manager", async () => {
    loginAs([SHIFT_MANAGER_ID])
    render(<DashboardClient />, { wrapper: Wrapper })

    expect(await screen.findByText("₺280,00")).toBeInTheDocument()
    expect(screen.getByText("Nakit")).toBeInTheDocument()
  })

  it("hides the report section for a cashier (no pos.report.read)", async () => {
    loginAs([CASHIER_ID])
    render(<DashboardClient />, { wrapper: Wrapper })

    expect(await screen.findByText("Bu rapor için Shift Müdürü yetkisi gerekir.")).toBeInTheDocument()
    expect(screen.queryByText("₺280,00")).not.toBeInTheDocument()
  })

  it("requests a new range when the period preset changes", async () => {
    loginAs([SHIFT_MANAGER_ID])
    render(<DashboardClient />, { wrapper: Wrapper })

    await screen.findByText("₺280,00")
    const reportCallsBefore = get.mock.calls.filter(([url]) => url === "/api/v1/pos/reports/sale-details")
    const [, firstConfig] = reportCallsBefore[reportCallsBefore.length - 1] as [string, { params: { from: string; to: string } }]

    fireEvent.click(screen.getByRole("button", { name: "Son 7 gün" }))

    await waitFor(() => {
      const reportCallsAfter = get.mock.calls.filter(([url]) => url === "/api/v1/pos/reports/sale-details")
      const [, lastConfig] = reportCallsAfter[reportCallsAfter.length - 1] as [string, { params: { from: string; to: string } }]
      expect(lastConfig.params.from).not.toBe(firstConfig.params.from)
    })
  })

  // Regression for the stale-past-midnight bug: a dashboard left open on
  // "today" across local midnight must roll its request range forward on its
  // own, with no preset click and no reload. Only Date/setInterval are faked
  // (`toFake`) so Testing Library's findByText/waitFor — which poll via a
  // REAL setTimeout — keep working normally instead of deadlocking.
  it("refreshes the range across local midnight while the dashboard stays open", async () => {
    vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] })
    vi.setSystemTime(new Date(2026, 8, 5, 23, 59, 30))

    try {
      loginAs([SHIFT_MANAGER_ID])
      render(<DashboardClient />, { wrapper: Wrapper })

      await screen.findByText("₺280,00")
      const before = get.mock.calls.filter(([url]) => url === "/api/v1/pos/reports/sale-details").length

      act(() => {
        vi.advanceTimersByTime(60_000)
      })

      await waitFor(() => {
        const after = get.mock.calls.filter(([url]) => url === "/api/v1/pos/reports/sale-details").length
        expect(after).toBeGreaterThan(before)
      })

      const reportCalls = get.mock.calls.filter(([url]) => url === "/api/v1/pos/reports/sale-details")
      const [, lastConfig] = reportCalls[reportCalls.length - 1] as [string, { params: { from: string } }]
      expect(lastConfig.params.from).toBe(new Date(2026, 8, 6).toISOString())
    } finally {
      vi.useRealTimers()
    }
  })
})
