// Component test for the redesigned admin products list page: search box,
// category chips, status filter, row navigation to the product detail route,
// and the row kebab menu's delete flow (destructive ConfirmDialog whose
// secondary action deactivates instead of deleting).
//
// Radix DropdownMenu opens on the trigger's `pointerdown` (not `click`, see
// @radix-ui/react-dropdown-menu's Trigger), so opening the kebab menu needs
// `fireEvent.pointerDown`. Its Content is positioned via @radix-ui/react-popper,
// which measures the trigger with a ResizeObserver — jsdom has none, hence the
// stub (same pattern as dashboard.test.tsx).
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import ProductsPage from "@/app/(main)/catalog/products/page"
import messages from "@/messages/tr.json"
import type { Category, Product } from "@/types"

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal("ResizeObserver", ResizeObserverStub)

const push = vi.fn()
const replace = vi.fn()
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push, replace }),
  usePathname: () => "/catalog/products",
  useSearchParams: () => new URLSearchParams(),
}))

const get = vi.fn()
const post = vi.fn()
const put = vi.fn()
const del = vi.fn()
vi.mock("@/lib/api", () => ({
  default: {
    get: (...args: unknown[]) => get(...args),
    post: (...args: unknown[]) => post(...args),
    put: (...args: unknown[]) => put(...args),
    delete: (...args: unknown[]) => del(...args),
  },
}))

const CATEGORY_ID = "cat-1"

const PRODUCTS: Product[] = [
  {
    id: "p1",
    tenant_id: "t1",
    branch_id: null,
    category_id: CATEGORY_ID,
    name: "Latte",
    description: "Sütlü espresso",
    image_key: "",
    price_amount: 15000,
    currency: "TRY",
    sku: "",
    unit: "adet",
    tax_rate_bps: 1000,
    is_active: true,
    sort_order: 0,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "p2",
    tenant_id: "t1",
    branch_id: null,
    category_id: null,
    name: "Su",
    description: "Şişe su",
    image_key: "",
    price_amount: 2000,
    currency: "TRY",
    sku: "",
    unit: "adet",
    tax_rate_bps: 100,
    is_active: false,
    sort_order: 1,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
]

const CATEGORIES: Category[] = [
  {
    id: CATEGORY_ID,
    tenant_id: "t1",
    name: "Sıcak İçecekler",
    description: "",
    sort_order: 0,
    is_active: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
]

const MODIFIER_GROUP = {
  id: "grp-1",
  tenant_id: "t1",
  name: "Boy Seçimi",
  selection_type: "single" as const,
  min_selections: 1,
  max_selections: 1,
  is_required: true,
  sort_order: 0,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
}

function mockRoutes() {
  get.mockImplementation((url: string) => {
    if (url === "/api/v1/catalog/products") return Promise.resolve({ data: PRODUCTS })
    if (url === "/api/v1/catalog/categories") return Promise.resolve({ data: CATEGORIES })
    if (url === "/api/v1/catalog/modifier-groups") return Promise.resolve({ data: [MODIFIER_GROUP] })
    if (url === `/api/v1/catalog/products/${PRODUCTS[0].id}/modifier-groups`)
      return Promise.resolve({ data: [MODIFIER_GROUP.id] })
    if (url.endsWith("/modifier-groups")) return Promise.resolve({ data: [] })
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

// Radix DropdownMenu's Trigger opens on pointerdown, not click.
function openRowMenu(name: string) {
  fireEvent.pointerDown(screen.getByRole("button", { name }), { button: 0, ctrlKey: false })
}

describe("ProductsPage", () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    put.mockReset()
    del.mockReset()
    push.mockReset()
    replace.mockReset()
    mockRoutes()
  })

  it("renders the product list with count summary, category and price", async () => {
    render(<ProductsPage />, { wrapper: Wrapper })

    expect(await screen.findByText("Latte")).toBeInTheDocument()
    expect(screen.getByText("Su")).toBeInTheDocument()
    expect(screen.getByText("2 ürün · 1 satışta")).toBeInTheDocument()
    // Appears twice: once as the filter chip, once as the row's category cell.
    expect(screen.getAllByText("Sıcak İçecekler")).toHaveLength(2)
    expect(screen.getByText("Kategorisiz")).toBeInTheDocument()
    expect(screen.getByText("₺150,00")).toBeInTheDocument()
  })

  it("narrows the list via the search box", async () => {
    render(<ProductsPage />, { wrapper: Wrapper })
    await screen.findByText("Latte")

    fireEvent.change(screen.getByPlaceholderText("Ürün ara"), { target: { value: "latte" } })

    expect(screen.getByText("Latte")).toBeInTheDocument()
    expect(screen.queryByText("Su")).not.toBeInTheDocument()
  })

  it("marks the selected category chip with aria-pressed", async () => {
    render(<ProductsPage />, { wrapper: Wrapper })
    await screen.findByText("Latte")

    expect(screen.getByRole("button", { name: "Tümü" })).toHaveAttribute("aria-pressed", "true")
    expect(screen.getByRole("button", { name: "Sıcak İçecekler" })).toHaveAttribute("aria-pressed", "false")

    fireEvent.click(screen.getByRole("button", { name: "Sıcak İçecekler" }))

    expect(screen.getByRole("button", { name: "Sıcak İçecekler" })).toHaveAttribute("aria-pressed", "true")
    expect(screen.getByRole("button", { name: "Tümü" })).toHaveAttribute("aria-pressed", "false")
  })

  it("shows the option-group badge for a product, resolved by group id", async () => {
    render(<ProductsPage />, { wrapper: Wrapper })

    expect(await screen.findByText(MODIFIER_GROUP.name)).toBeInTheDocument()
  })

  it("filters by category chip", async () => {
    render(<ProductsPage />, { wrapper: Wrapper })
    await screen.findByText("Latte")

    fireEvent.click(screen.getByRole("button", { name: "Kategorisiz · 1" }))

    expect(screen.queryByText("Latte")).not.toBeInTheDocument()
    expect(screen.getByText("Su")).toBeInTheDocument()
  })

  it("navigates to the product detail route when a row is clicked", async () => {
    render(<ProductsPage />, { wrapper: Wrapper })
    await screen.findByText("Latte")

    fireEvent.click(screen.getByText("Latte"))

    expect(push).toHaveBeenCalledWith("/catalog/products/p1")
  })

  it("deletes a product after confirming the destructive dialog", async () => {
    del.mockResolvedValue({})
    render(<ProductsPage />, { wrapper: Wrapper })
    await screen.findByText("Latte")

    openRowMenu("Latte için işlemler")
    fireEvent.click(await screen.findByRole("menuitem", { name: "Sil" }))

    const dialog = await screen.findByRole("alertdialog")
    fireEvent.click(within(dialog).getByRole("button", { name: "Sil" }))

    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/v1/catalog/products/p1"))
  })

  it("sends the full product body when toggling active state from the row kebab menu", async () => {
    put.mockResolvedValue({ data: { ...PRODUCTS[0], is_active: false } })
    render(<ProductsPage />, { wrapper: Wrapper })
    await screen.findByText("Latte")

    openRowMenu("Latte için işlemler")
    fireEvent.click(await screen.findByRole("menuitem", { name: "Satıştan kaldır" }))

    // Backend PUT REPLACES the whole row — a body missing any field would
    // zero it out server-side (currency, source_stock_item_id included).
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/v1/catalog/products/p1", {
        category_id: PRODUCTS[0].category_id,
        name: PRODUCTS[0].name,
        description: PRODUCTS[0].description,
        price_amount: PRODUCTS[0].price_amount,
        currency: PRODUCTS[0].currency,
        unit: PRODUCTS[0].unit,
        tax_rate_bps: PRODUCTS[0].tax_rate_bps,
        is_active: false,
        sort_order: PRODUCTS[0].sort_order,
        source_stock_item_id: null,
      }),
    )
  })

  it("deactivates instead of deleting via the confirm dialog's secondary action, sending the full body", async () => {
    put.mockResolvedValue({ data: { ...PRODUCTS[0], is_active: false } })
    render(<ProductsPage />, { wrapper: Wrapper })
    await screen.findByText("Latte")

    openRowMenu("Latte için işlemler")
    fireEvent.click(await screen.findByRole("menuitem", { name: "Sil" }))

    const dialog = await screen.findByRole("alertdialog")
    fireEvent.click(within(dialog).getByRole("button", { name: "Satıştan kaldır" }))

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/v1/catalog/products/p1", {
        category_id: PRODUCTS[0].category_id,
        name: PRODUCTS[0].name,
        description: PRODUCTS[0].description,
        price_amount: PRODUCTS[0].price_amount,
        currency: PRODUCTS[0].currency,
        unit: PRODUCTS[0].unit,
        tax_rate_bps: PRODUCTS[0].tax_rate_bps,
        is_active: false,
        sort_order: PRODUCTS[0].sort_order,
        source_stock_item_id: null,
      }),
    )
    expect(del).not.toHaveBeenCalled()
  })
})
