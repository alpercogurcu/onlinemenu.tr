// "Bu grubu kullanan ürünler" on the seçenek grubu page. A manager built
// "Patates Seçimi" and could not attach it to a burger (2026-09-23): this
// card was read-only and said only "Henüz hiçbir üründe kullanılmıyor", while
// the one working path sat on each product's page. The group page now
// assigns and removes products itself (same endpoints as the product page).
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import messages from "@/messages/tr.json"

const GROUP_ID = "11111111-0000-0000-0000-000000000001"

let canAssign = true
vi.mock("@/hooks/use-can", () => ({ useCan: () => canAssign }))

const get = vi.fn()
const post = vi.fn()
const del = vi.fn()
vi.mock("@/lib/api", () => ({
  default: {
    get: (...a: unknown[]) => get(...a),
    post: (...a: unknown[]) => post(...a),
    delete: (...a: unknown[]) => del(...a),
  },
}))
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn() }) }))

// cmdk measures/scrolls items; jsdom lacks both.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal("ResizeObserver", ResizeObserverStub)
Element.prototype.scrollIntoView = vi.fn()

import { GroupProductsCard } from "@/components/catalog/group-products-card"

let assigned: string[] = []
const products = [
  { id: "p-smash", name: "American Smash Burger", is_active: true },
  { id: "p-cheese", name: "Cheese Burger", is_active: true },
]

function Wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <NextIntlClientProvider locale="tr" messages={messages}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </NextIntlClientProvider>
  )
}

describe("GroupProductsCard", () => {
  beforeEach(() => {
    canAssign = true
    assigned = []
    get.mockReset()
    post.mockReset()
    del.mockReset()
    get.mockImplementation((url: string) => {
      if (url === `/api/v1/catalog/modifier-groups/${GROUP_ID}/products`) return Promise.resolve({ data: assigned })
      if (url === "/api/v1/catalog/products") return Promise.resolve({ data: products })
      return Promise.resolve({ data: [] })
    })
    post.mockResolvedValue({ data: null })
    del.mockResolvedValue({ data: null })
  })

  it("assigns the group to a product picked on the group page", async () => {
    render(<GroupProductsCard groupId={GROUP_ID} />, { wrapper: Wrapper })

    fireEvent.click(await screen.findByRole("button", { name: "Ürüne ekle" }))
    fireEvent.click(await screen.findByRole("option", { name: "American Smash Burger" }))

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/v1/catalog/products/p-smash/modifier-groups", {
        group_id: GROUP_ID,
        sort_order: 0,
      }),
    )
  })

  it("removes the group from a product", async () => {
    assigned = ["p-cheese"]
    render(<GroupProductsCard groupId={GROUP_ID} />, { wrapper: Wrapper })

    fireEvent.click(await screen.findByRole("button", { name: "Cheese Burger — gruptan çıkar" }))
    await waitFor(() =>
      expect(del).toHaveBeenCalledWith(`/api/v1/catalog/products/p-cheese/modifier-groups/${GROUP_ID}`),
    )
  })

  it("tells an empty group how to get used", async () => {
    render(<GroupProductsCard groupId={GROUP_ID} />, { wrapper: Wrapper })
    expect(await screen.findByText(/“Ürüne ekle” ile/)).toBeInTheDocument()
  })

  it("is read-only without catalog.modifier_group.assign", async () => {
    canAssign = false
    assigned = ["p-cheese"]
    render(<GroupProductsCard groupId={GROUP_ID} />, { wrapper: Wrapper })

    expect(await screen.findByText("Cheese Burger")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Ürüne ekle" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /gruptan çıkar/ })).not.toBeInTheDocument()
  })
})
