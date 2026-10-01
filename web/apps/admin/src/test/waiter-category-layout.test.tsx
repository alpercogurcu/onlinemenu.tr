// Branch-configurable category layout on the waiter order screen:
// 'side' renders the vertical rail (and it filters products), 'top' keeps the
// chip strip, a failing/missing GET /pos/branch-settings falls back to 'top'
// (fail-open — the layout is cosmetic), and narrow screens force 'top' even
// when the branch prefers 'side'.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { OrderScreen } from "@/components/pos/order/order-screen"
import messages from "@/messages/tr.json"

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/pos/order",
}))
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const get = vi.fn()
const post = vi.fn()
vi.mock("@/lib/api", () => ({
  default: {
    get: (...args: unknown[]) => get(...args),
    post: (...args: unknown[]) => post(...args),
  },
}))

// useIsMobile reads matchMedia + innerWidth; jsdom implements neither the
// former nor lets the latter follow a real viewport. Width is per-test.
function setViewportWidth(width: number) {
  Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: width })
}

vi.stubGlobal(
  "matchMedia",
  vi.fn().mockImplementation((query: string) => ({
    matches: query.includes("max-width") ? window.innerWidth < 768 : false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })),
)

const BRANCH = "b1"
const TABLE = { id: "t1", branch_id: BRANCH, zone_id: "z1", name: "Masa 4", capacity: 4, status: "empty", layout_position: null, is_active: true, active_check_id: null }
const stamp = { tenant_id: "tn", created_at: "", updated_at: "" }
const category = (id: string, name: string, sort: number) => ({ ...stamp, id, name, description: "", sort_order: sort, is_active: true })
const product = (id: string, name: string, categoryId: string, sort: number) => ({
  ...stamp, id, name, price_amount: 10_000, category_id: categoryId, branch_id: null, description: "", image_key: "",
  currency: "TRY", sku: "", unit: "adet", tax_rate_bps: 1000, is_active: true, sort_order: sort,
})

function routeGet(url: string, layout?: unknown) {
  const map: Record<string, unknown> = {
    "/api/v1/pos/tables": [{ zone_id: "z1", zone_name: "Salon", floor: 0, tables: [TABLE] }],
    "/api/v1/pos/branch-settings": layout === undefined ? {} : { waiter_category_layout: layout },
    "/api/v1/catalog/categories": [category("mains", "Ana Yemekler", 1), category("drinks", "İçecekler", 2)],
    "/api/v1/catalog/products": [product("kebap", "Adana Kebap", "mains", 1), product("ayran", "Ayran", "drinks", 1)],
    "/api/v1/catalog/products/modifier-groups": [],
  }
  if (!(url in map)) return Promise.reject(new Error(`unexpected GET ${url}`))
  return Promise.resolve({ data: map[url] })
}

function mockLayout(layout?: unknown) {
  get.mockImplementation((url: string) => routeGet(url, layout))
}

function wrap(children: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <QueryClientProvider client={qc}>
      <NextIntlClientProvider locale="tr" messages={messages}>
        {children}
      </NextIntlClientProvider>
    </QueryClientProvider>
  )
}

function renderScreen() {
  return render(wrap(<OrderScreen branchId={BRANCH} tableId="t1" onBackToTables={vi.fn()} />))
}

const tile = (name: string) => screen.findByRole("button", { name: new RegExp(`^${name},`) })

beforeEach(() => {
  setViewportWidth(1024)
  get.mockReset()
  post.mockReset()
})

describe("waiter category layout", { timeout: 20_000 }, () => {
  it("side: renders the vertical rail instead of the chip strip, with 48px touch items", async () => {
    mockLayout("side")
    renderScreen()
    await tile("Adana Kebap")

    const rail = screen.getByTestId("category-rail")
    expect(rail).toHaveAttribute("aria-orientation", "vertical")
    expect(screen.queryByTestId("category-chips")).not.toBeInTheDocument()

    const active = screen.getByRole("tab", { name: "Ana Yemekler" })
    expect(active).toHaveAttribute("aria-selected", "true")
    expect(active.className).toContain("min-h-12")
    expect(get).toHaveBeenCalledWith("/api/v1/pos/branch-settings", { params: { branch_id: BRANCH } })
  })

  it("side: tapping a rail category filters the product grid", async () => {
    mockLayout("side")
    renderScreen()
    await tile("Adana Kebap")
    expect(screen.queryByRole("button", { name: /^Ayran,/ })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole("tab", { name: "İçecekler" }))
    await tile("Ayran")
    expect(screen.queryByRole("button", { name: /^Adana Kebap,/ })).not.toBeInTheDocument()
    expect(screen.getByRole("tab", { name: "İçecekler" })).toHaveAttribute("aria-selected", "true")
  })

  it("top: keeps the horizontal chip strip, no rail", async () => {
    mockLayout("top")
    renderScreen()
    await tile("Adana Kebap")

    expect(screen.getByTestId("category-chips")).toBeInTheDocument()
    expect(screen.queryByTestId("category-rail")).not.toBeInTheDocument()
  })

  it("a failing branch-settings endpoint falls back to top (fail-open)", async () => {
    get.mockImplementation((url: string) =>
      url === "/api/v1/pos/branch-settings" ? Promise.reject(new Error("boom")) : routeGet(url),
    )
    renderScreen()
    await tile("Adana Kebap")

    expect(screen.getByTestId("category-chips")).toBeInTheDocument()
    expect(screen.queryByTestId("category-rail")).not.toBeInTheDocument()
  })

  it("a response without the field falls back to top", async () => {
    mockLayout(undefined)
    renderScreen()
    await tile("Adana Kebap")

    expect(screen.getByTestId("category-chips")).toBeInTheDocument()
    expect(screen.queryByTestId("category-rail")).not.toBeInTheDocument()
  })

  it("side on a narrow screen (below md) falls back to the top chips", async () => {
    setViewportWidth(390)
    mockLayout("side")
    renderScreen()
    await tile("Adana Kebap")

    await waitFor(() => expect(screen.getByTestId("category-chips")).toBeInTheDocument())
    expect(screen.queryByTestId("category-rail")).not.toBeInTheDocument()
  })
})
