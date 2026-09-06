// Contract tests for the product detail editor. The risky spots are the same
// as elsewhere in this module: the money unit (kuruş vs lira), the tax
// gross-up formula's live hint, and the two-step "create group, then assign
// it" flow the combobox's "new group" option triggers.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { ProductEditor } from "@/components/catalog/product-editor"
import messages from "@/messages/tr.json"
import type { Category, Menu, MenuItem, ModifierGroup, Product } from "@/types"

// cmdk (used by the "add group" combobox) calls scrollIntoView while
// navigating its item list, and observes its list size with a
// ResizeObserver — neither exists in jsdom.
Element.prototype.scrollIntoView = vi.fn()
global.ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const TENANT = "aaaaaaaa-0000-0000-0000-000000000001"

const CATEGORY: Category = {
  id: "cat-1",
  tenant_id: TENANT,
  name: "Ana Yemekler",
  description: "",
  sort_order: 0,
  is_active: true,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
}

const PRODUCT: Product = {
  id: "prod-1",
  tenant_id: TENANT,
  branch_id: null,
  category_id: CATEGORY.id,
  name: "Adana Kebap",
  description: "Acılı",
  image_key: "",
  price_amount: 15000,
  currency: "TRY",
  sku: "",
  unit: "adet",
  tax_rate_bps: 1000,
  is_active: true,
  sort_order: 2,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
}

const GROUP_ASSIGNED: ModifierGroup = {
  id: "grp-1",
  tenant_id: TENANT,
  name: "Boy Seçimi",
  selection_type: "single",
  min_selections: 1,
  max_selections: 1,
  is_required: true,
  sort_order: 0,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
}

const GROUP_UNASSIGNED: ModifierGroup = {
  id: "grp-2",
  tenant_id: TENANT,
  name: "Sos",
  selection_type: "multiple",
  min_selections: 0,
  max_selections: null,
  is_required: false,
  sort_order: 1,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
}

const MENU: Menu = {
  id: "menu-1",
  tenant_id: TENANT,
  name: "Standart Menü",
  description: "",
  is_active: true,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
}

let assignedGroupIds: string[] = []
let menuItems: MenuItem[] = []

const post = vi.fn()
const put = vi.fn()
const del = vi.fn()

vi.mock("@/lib/api", () => ({
  default: {
    get: (url: string) => {
      if (url === `/api/v1/catalog/products/${PRODUCT.id}`) return Promise.resolve({ data: PRODUCT })
      if (url === "/api/v1/catalog/categories") return Promise.resolve({ data: [CATEGORY] })
      if (url === "/api/v1/catalog/menus") return Promise.resolve({ data: [MENU] })
      if (url === `/api/v1/catalog/menus/${MENU.id}/items`) return Promise.resolve({ data: menuItems })
      if (url === `/api/v1/catalog/products/${PRODUCT.id}/modifier-groups`)
        return Promise.resolve({ data: assignedGroupIds })
      if (url === "/api/v1/catalog/modifier-groups")
        return Promise.resolve({ data: [GROUP_ASSIGNED, GROUP_UNASSIGNED] })
      if (url.endsWith("/modifiers")) return Promise.resolve({ data: [] })
      return Promise.resolve({ data: [] })
    },
    post: (...args: unknown[]) => post(...args),
    put: (...args: unknown[]) => put(...args),
    delete: (...args: unknown[]) => del(...args),
  },
}))

const push = vi.fn()
const replace = vi.fn()
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push, replace, back: vi.fn() }),
}))

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

function Wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <NextIntlClientProvider locale="tr" messages={messages}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </NextIntlClientProvider>
  )
}

// `productId` defaults to editing PRODUCT.id; pass an explicit
// `{ productId: undefined }` override for new-product mode — a bare
// `renderEditor(undefined)` would not work, since JS default parameters also
// kick in for an explicitly-passed `undefined` argument.
function renderEditor(overrides: { productId?: string } = {}) {
  const productId = "productId" in overrides ? overrides.productId : PRODUCT.id
  return render(<ProductEditor productId={productId} />, { wrapper: Wrapper })
}

describe("ProductEditor", () => {
  beforeEach(() => {
    assignedGroupIds = [GROUP_ASSIGNED.id]
    menuItems = []
    post.mockReset()
    post.mockImplementation((url: string) => {
      if (url === "/api/v1/catalog/modifier-groups") {
        return Promise.resolve({ data: { ...GROUP_UNASSIGNED, id: "grp-new" } })
      }
      if (url === "/api/v1/catalog/products") {
        return Promise.resolve({ data: { ...PRODUCT, id: "prod-new-1" } })
      }
      return Promise.resolve({ data: undefined })
    })
    put.mockReset()
    put.mockResolvedValue({ data: undefined })
    del.mockReset()
    del.mockResolvedValue({ data: undefined })
    push.mockReset()
    replace.mockReset()
  })

  it("loads the form pre-filled in edit mode", async () => {
    renderEditor()

    expect(await screen.findByLabelText("Ad *")).toHaveValue(PRODUCT.name)
    expect(screen.getByLabelText("Açıklama")).toHaveValue(PRODUCT.description)
    expect(screen.getByLabelText("Birim")).toHaveValue(PRODUCT.unit)
    expect(screen.getByLabelText("Sıra")).toHaveValue(PRODUCT.sort_order)
    expect(screen.getByLabelText("Kategori")).toHaveValue(CATEGORY.id)
    expect(screen.getByLabelText("KDV oranı")).toHaveValue(String(PRODUCT.tax_rate_bps))
    expect(screen.getByLabelText("Satışta")).toBeChecked()
  })

  it("blocks save and shows an inline error when the name is cleared", async () => {
    renderEditor()

    const nameInput = await screen.findByLabelText("Ad *")
    fireEvent.change(nameInput, { target: { value: "" } })
    fireEvent.click(screen.getByRole("button", { name: "Kaydet" }))

    expect(await screen.findByText("Ürün adı zorunludur")).toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  })

  it("converts a typed lira price into kuruş on create", async () => {
    renderEditor({ productId: undefined })

    fireEvent.change(await screen.findByLabelText("Ad *"), { target: { value: "Yeni Ürün" } })
    fireEvent.change(screen.getByLabelText("Fiyat (KDV dahil) *"), { target: { value: "150" } })
    fireEvent.click(screen.getByRole("button", { name: "Kaydet" }))

    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/catalog/products", expect.anything()))
    expect(post).toHaveBeenCalledWith(
      "/api/v1/catalog/products",
      expect.objectContaining({ name: "Yeni Ürün", price_amount: 15000 }),
    )
  })

  it("shows the live base/tax hint for a 10% rate", async () => {
    renderEditor()

    expect(await screen.findByText("Matrah ₺136,36 · KDV ₺13,64")).toBeInTheDocument()
  })

  it("assigns an existing group from the combobox", async () => {
    renderEditor()

    fireEvent.click(await screen.findByRole("button", { name: "Grup ara veya ekle" }))
    fireEvent.click(await screen.findByText(GROUP_UNASSIGNED.name))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith(`/api/v1/catalog/products/${PRODUCT.id}/modifier-groups`, {
      group_id: GROUP_UNASSIGNED.id,
      sort_order: 0,
    })
  })

  it("creates then assigns a brand new group typed into the combobox", async () => {
    renderEditor()

    fireEvent.click(await screen.findByRole("button", { name: "Grup ara veya ekle" }))
    const input = await screen.findByPlaceholderText("Grup ara veya ekle")
    fireEvent.change(input, { target: { value: "Ekstra" } })

    fireEvent.click(await screen.findByText('Yeni grup: "Ekstra"'))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
    expect(post).toHaveBeenNthCalledWith(1, "/api/v1/catalog/modifier-groups", {
      name: "Ekstra",
      selection_type: "single",
      min_selections: 0,
      max_selections: 1,
      is_required: false,
    })
    expect(post).toHaveBeenNthCalledWith(2, `/api/v1/catalog/products/${PRODUCT.id}/modifier-groups`, {
      group_id: "grp-new",
      sort_order: 0,
    })
  })

  it("adds the product to a menu via its switch", async () => {
    renderEditor()

    fireEvent.click(await screen.findByRole("checkbox", { name: MENU.name }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith(`/api/v1/catalog/menus/${MENU.id}/items`, {
      product_id: PRODUCT.id,
      price_override: null,
      is_active: true,
    })
  })

  it("deletes the product after confirmation and returns to the list", async () => {
    renderEditor()

    await screen.findByLabelText("Ad *")
    fireEvent.click(screen.getByRole("button", { name: "Sil" }))

    const dialog = await screen.findByRole("alertdialog")
    fireEvent.click(within(dialog).getByRole("button", { name: "Sil" }))

    await waitFor(() => expect(del).toHaveBeenCalledWith(`/api/v1/catalog/products/${PRODUCT.id}`))
    await waitFor(() => expect(push).toHaveBeenCalledWith("/catalog/products"))
  })

  it("deactivates instead of deleting via the delete dialog's secondary action, sending the full product body", async () => {
    renderEditor()

    await screen.findByLabelText("Ad *")
    fireEvent.click(screen.getByRole("button", { name: "Sil" }))

    const dialog = await screen.findByRole("alertdialog")
    fireEvent.click(within(dialog).getByRole("button", { name: "Satıştan kaldır" }))

    // Backend PUT REPLACES the whole row — a body with only is_active would
    // zero out every other field, so this must carry the complete product.
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        `/api/v1/catalog/products/${PRODUCT.id}`,
        expect.objectContaining({
          name: PRODUCT.name,
          price_amount: PRODUCT.price_amount,
          unit: PRODUCT.unit,
          tax_rate_bps: PRODUCT.tax_rate_bps,
          category_id: PRODUCT.category_id,
          sort_order: PRODUCT.sort_order,
          currency: PRODUCT.currency,
          is_active: false,
        }),
      ),
    )
    expect(del).not.toHaveBeenCalled()
  })
})
