// Contract test for menu item management. The two things that can silently go
// wrong here are the money unit (the API stores kuruş, the operator types lira)
// and the "no override" case — an empty price field must send null, not 0,
// because 0 would put the product on the menu for free.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { MenuItemsDialog } from "@/components/catalog/menu-items-dialog"
import messages from "@/messages/tr.json"
import type { Menu, MenuItem, Product } from "@/types"

const MENU: Menu = {
  id: "11b97566-6e08-4403-80d1-ad17b99edd8c",
  tenant_id: "aaaaaaaa-0000-0000-0000-000000000001",
  name: "Standart Menü",
  description: "",
  is_active: true,
  created_at: "2026-07-06T07:22:39.523931Z",
  updated_at: "2026-07-06T07:22:39.523931Z",
}

const PRODUCTS: Product[] = [
  {
    id: "9b009f20-4546-4958-b0aa-4051b2f53c16",
    tenant_id: MENU.tenant_id,
    branch_id: null,
    category_id: null,
    name: "Adana Kebap",
    description: "",
    image_key: "",
    price_amount: 16000,
    currency: "TRY",
    sku: "",
    unit: "adet",
    tax_rate_bps: 0,
    is_active: true,
    sort_order: 0,
    created_at: "2026-07-06T07:42:57.149694Z",
    updated_at: "2026-07-06T07:42:57.149694Z",
  },
]

const PLACED: MenuItem[] = [
  {
    menu_id: MENU.id,
    product_id: PRODUCTS[0].id,
    tenant_id: MENU.tenant_id,
    price_override: null,
    is_active: true,
  },
]

let menuItems: MenuItem[] = []
const post = vi.fn()
const del = vi.fn()

vi.mock("@/lib/api", () => ({
  default: {
    get: (url: string) => {
      if (url.endsWith("/items")) return Promise.resolve({ data: menuItems })
      return Promise.resolve({ data: PRODUCTS })
    },
    post: (...args: unknown[]) => post(...args),
    delete: (...args: unknown[]) => del(...args),
  },
}))

const toastError = vi.fn()
vi.mock("sonner", () => ({
  toast: {
    success: vi.fn(),
    error: (...args: unknown[]) => toastError(...args),
  },
}))

function Wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <NextIntlClientProvider locale="tr" messages={messages}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </NextIntlClientProvider>
  )
}

function renderSheet() {
  return render(<MenuItemsDialog open onOpenChange={() => {}} menu={MENU} />, { wrapper: Wrapper })
}

// The <select> element exists before its options do (the product query is still
// in flight), and setting a value that has no matching <option> is a no-op —
// so the option itself is what the test has to wait for.
async function pickProduct(name: string, id: string) {
  await screen.findByRole("option", { name: new RegExp(name) })
  fireEvent.change(screen.getByLabelText("Ürün"), { target: { value: id } })
}

describe("MenuItemsDialog", () => {
  beforeEach(() => {
    menuItems = []
    post.mockReset()
    post.mockResolvedValue({ data: undefined })
    del.mockReset()
    del.mockResolvedValue({ data: undefined })
    toastError.mockReset()
  })

  it("converts a lira price override into kuruş", async () => {
    renderSheet()

    await pickProduct("Adana Kebap", PRODUCTS[0].id)
    fireEvent.change(screen.getByLabelText("Menüye özel fiyat (₺)"), {
      target: { value: "149,90" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Menüye ekle" }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith(`/api/v1/catalog/menus/${MENU.id}/items`, {
      product_id: PRODUCTS[0].id,
      price_override: 14990,
      is_active: true,
    })
  })

  it("sends null (not zero) when no override is typed", async () => {
    renderSheet()

    await pickProduct("Adana Kebap", PRODUCTS[0].id)
    fireEvent.click(screen.getByRole("button", { name: "Menüye ekle" }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith(
      `/api/v1/catalog/menus/${MENU.id}/items`,
      expect.objectContaining({ price_override: null }),
    )
  })

  it("refuses an unparseable price instead of clearing the override", async () => {
    renderSheet()

    await pickProduct("Adana Kebap", PRODUCTS[0].id)
    fireEvent.change(screen.getByLabelText("Menüye özel fiyat (₺)"), {
      target: { value: "yüz lira" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Menüye ekle" }))

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Fiyat geçersiz. Örnek: 149,90"))
    expect(post).not.toHaveBeenCalled()
  })

  it("requires a product to be picked", async () => {
    renderSheet()

    await screen.findByLabelText("Ürün")
    fireEvent.click(screen.getByRole("button", { name: "Menüye ekle" }))

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Ürün seçilmelidir"))
    expect(post).not.toHaveBeenCalled()
  })

  it("lists a placed item with the product's own price when it has no override", async () => {
    menuItems = PLACED
    renderSheet()

    expect(await screen.findByText("Adana Kebap")).toBeInTheDocument()
    expect(screen.getByText("₺160,00")).toBeInTheDocument()
  })

  it("removes a placed item by product id", async () => {
    menuItems = PLACED
    renderSheet()

    fireEvent.click(await screen.findByRole("button", { name: "Adana Kebap ürününü menüden çıkar" }))

    await waitFor(() => expect(del).toHaveBeenCalledTimes(1))
    expect(del).toHaveBeenCalledWith(
      `/api/v1/catalog/menus/${MENU.id}/items/${PRODUCTS[0].id}`,
    )
  })
})
