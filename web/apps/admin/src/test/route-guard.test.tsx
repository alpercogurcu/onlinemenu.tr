// RouteGuard: a forbidden screen renders the access-denied state INSTEAD of
// the page, so none of the page's queries fire. CheckDetail: a waiter sees the
// items but the settlement (money) request is never issued.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import RouteGuard from "@/components/auth/route-guard"
import { CheckDetail } from "@/components/pos/check-detail"
import messages from "@/messages/tr.json"
import { useAuthStore } from "@/store/auth-store"
import type { Check, Order } from "@/types"

const nav = vi.hoisted(() => ({ pathname: "/", replace: vi.fn(), push: vi.fn() }))
vi.mock("next/navigation", () => ({
  usePathname: () => nav.pathname,
  useRouter: () => ({ replace: nav.replace, push: nav.push }),
}))

const token = vi.hoisted(() => ({ value: null as string | null }))
const get = vi.fn()
vi.mock("@/lib/api", () => ({
  default: { get: (...args: unknown[]) => get(...args), post: vi.fn() },
  getAccessToken: () => token.value,
  setAccessToken: (t: string) => {
    token.value = t
  },
  clearAccessToken: () => {
    token.value = null
  },
}))

const ROLE = {
  manager: "00000001-0000-0000-0000-000000000006",
  cashier: "00000001-0000-0000-0000-000000000001",
  waiter: "00000001-0000-0000-0000-000000000008",
  kitchen: "00000001-0000-0000-0000-000000000004",
}

function signInAs(roleId: string) {
  const enc = (obj: unknown) => Buffer.from(JSON.stringify(obj)).toString("base64url")
  token.value = `${enc({ alg: "HS256", typ: "CTX" })}.${enc({ rids: [roleId] })}.sig`
  useAuthStore.setState({ user: { id: "u1", name: "U", email: "u@x" }, tenantId: "t1" })
}

function wrap(ui: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <NextIntlClientProvider locale="tr" messages={messages}>
      <QueryClientProvider client={qc}>{ui}</QueryClientProvider>
    </NextIntlClientProvider>,
  )
}

afterEach(() => {
  token.value = null
  useAuthStore.setState({ user: null, tenantId: null })
  get.mockReset()
  nav.replace.mockReset()
})

describe("RouteGuard", () => {
  it("renders the page for a permitted role", () => {
    signInAs(ROLE.waiter)
    nav.pathname = "/pos/tables"
    wrap(
      <RouteGuard>
        <p>tables page</p>
      </RouteGuard>,
    )
    expect(screen.getByText("tables page")).toBeInTheDocument()
  })

  it("shows access denied and does NOT mount the page for a forbidden role", () => {
    signInAs(ROLE.waiter)
    nav.pathname = "/catalog/products/abc"
    const Page = vi.fn(() => <p>editor</p>)
    wrap(
      <RouteGuard>
        <Page />
      </RouteGuard>,
    )
    expect(screen.getByText("Bu sayfaya erişim yetkiniz yok")).toBeInTheDocument()
    expect(screen.getByRole("link", { name: "Ana sayfama dön" })).toHaveAttribute("href", "/pos/order")
    expect(Page).not.toHaveBeenCalled()
  })

  it("forwards a role without dashboard access from / to its home", async () => {
    signInAs(ROLE.kitchen)
    nav.pathname = "/"
    const Page = vi.fn(() => <p>dashboard</p>)
    wrap(
      <RouteGuard>
        <Page />
      </RouteGuard>,
    )
    await waitFor(() => expect(nav.replace).toHaveBeenCalledWith("/pos/kitchen"))
    expect(Page).not.toHaveBeenCalled()
  })

  it("keeps the manager on the dashboard", () => {
    signInAs(ROLE.manager)
    nav.pathname = "/"
    wrap(
      <RouteGuard>
        <p>dashboard</p>
      </RouteGuard>,
    )
    expect(screen.getByText("dashboard")).toBeInTheDocument()
    expect(nav.replace).not.toHaveBeenCalled()
  })
})

const CHECK: Check = {
  id: "c1",
  tenant_id: "t1",
  branch_id: "b1",
  table_label: "Masa 4",
  pax: 2,
  status: "open",
  note: "",
  opened_at: "2026-09-23T10:00:00Z",
  closed_at: null,
  total: 25000,
}

const ORDERS: Order[] = [
  {
    id: "o1",
    check_id: "c1",
    tenant_id: "t1",
    branch_id: "b1",
    order_channel: "dine_in",
    status: "preparing",
    note: "",
    items: [
      {
        id: "i1",
        product_id: "p1",
        product_name: "Adana Kebap",
        quantity: 2,
        unit_price_amount: 12500,
        note: "acısız",
        modifier_ids: ["m1"],
      },
    ],
    created_at: "2026-09-23T10:05:00Z",
    updated_at: "2026-09-23T10:05:00Z",
  },
]

function stubApi() {
  get.mockImplementation((url: string) => {
    if (url === "/api/v1/pos/checks/c1") return Promise.resolve({ data: CHECK })
    if (url === "/api/v1/pos/checks/c1/orders") return Promise.resolve({ data: ORDERS })
    if (url === "/api/v1/payments/checks/c1/settlement")
      return Promise.resolve({
        data: { check_id: "c1", as_of: "", completed: [{ payment_id: "pay-0001", amount_total: 10000 }], pending_total: 0 },
      })
    return Promise.reject(new Error(`unexpected ${url}`))
  })
}

describe("CheckDetail", () => {
  beforeEach(stubApi)

  it("shows items and order state to a waiter without requesting the settlement", async () => {
    signInAs(ROLE.waiter)
    wrap(<CheckDetail checkId="c1" />)

    expect(await screen.findByText("Adana Kebap")).toBeInTheDocument()
    expect(screen.getByText("Hazırlanıyor")).toBeInTheDocument()
    expect(screen.getByText("acısız · +1 seçenek")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Kapat" })).not.toBeInTheDocument()
    expect(screen.queryByText("Kalan")).not.toBeInTheDocument()
    expect(get.mock.calls.map((c) => c[0])).not.toContain("/api/v1/payments/checks/c1/settlement")
  })

  it("shows payments, remaining and close/cancel to a cashier", async () => {
    signInAs(ROLE.cashier)
    wrap(<CheckDetail checkId="c1" />)

    expect(await screen.findByText("Kalan")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Kapat" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "İptal" })).toBeInTheDocument()
    expect(get.mock.calls.map((c) => c[0])).toContain("/api/v1/payments/checks/c1/settlement")
  })

  it("does not count a payment in flight as still owed", async () => {
    get.mockImplementation((url: string) => {
      if (url === "/api/v1/pos/checks/c1") return Promise.resolve({ data: CHECK })
      if (url === "/api/v1/pos/checks/c1/orders") return Promise.resolve({ data: ORDERS })
      return Promise.resolve({
        data: { check_id: "c1", as_of: "", completed: [{ payment_id: "pay-0001", amount_total: 10000 }], pending_total: 5000 },
      })
    })
    signInAs(ROLE.cashier)
    wrap(<CheckDetail checkId="c1" />)

    // total 250,00 − paid 100,00 − pending 50,00 = 100,00
    const remaining = await screen.findByText("Kalan")
    expect(remaining.parentElement).toHaveTextContent("₺100,00")
    expect(screen.getByText("Onay bekleyen ödeme").parentElement).toHaveTextContent("₺50,00")
  })
})
