// Component test for the seçenek grupları list page: rule-text summary per
// selection type, per-group option/product counts (batched useQueries), "Grup
// ekle" navigation, row navigation to the detail route, and the kebab menu's
// delete confirmation (whose body must report the group's actual product
// count, not a placeholder).
//
// Radix DropdownMenu's Trigger opens on pointerdown, not click (see
// products-page.test.tsx for the same note); its Content measures the
// trigger via a ResizeObserver, which jsdom doesn't implement.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import ModifiersPage from "@/app/(main)/catalog/modifiers/page"
import messages from "@/messages/tr.json"
import type { ModifierGroup } from "@/types"

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal("ResizeObserver", ResizeObserverStub)

const push = vi.fn()
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push, replace: vi.fn() }),
}))

const get = vi.fn()
const del = vi.fn()
vi.mock("@/lib/api", () => ({
  default: {
    get: (...args: unknown[]) => get(...args),
    post: vi.fn(),
    put: vi.fn(),
    delete: (...args: unknown[]) => del(...args),
  },
}))

const toastError = vi.fn()
const toastSuccess = vi.fn()
vi.mock("sonner", () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}))

const SINGLE_GROUP: ModifierGroup = {
  id: "g1",
  tenant_id: "t1",
  name: "Boy Seçimi",
  selection_type: "single",
  min_selections: 1,
  max_selections: 1,
  is_required: true,
  sort_order: 0,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
}

const MULTI_GROUP: ModifierGroup = {
  id: "g2",
  tenant_id: "t1",
  name: "Ek Malzeme",
  selection_type: "multiple",
  min_selections: 0,
  max_selections: 3,
  is_required: false,
  sort_order: 1,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
}

function mockRoutes() {
  get.mockImplementation((url: string) => {
    if (url === "/api/v1/catalog/modifier-groups") {
      return Promise.resolve({ data: [SINGLE_GROUP, MULTI_GROUP] })
    }
    if (url === "/api/v1/catalog/modifier-groups/g1/modifiers") {
      return Promise.resolve({ data: [{ id: "m1" }, { id: "m2" }] })
    }
    if (url === "/api/v1/catalog/modifier-groups/g2/modifiers") {
      return Promise.resolve({ data: [{ id: "m3" }, { id: "m4" }, { id: "m5" }, { id: "m6" }] })
    }
    if (url === "/api/v1/catalog/modifier-groups/g1/products") {
      return Promise.resolve({ data: ["p1", "p2", "p3"] })
    }
    if (url === "/api/v1/catalog/modifier-groups/g2/products") {
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

function openRowMenu(name: string) {
  fireEvent.pointerDown(screen.getByRole("button", { name }), { button: 0, ctrlKey: false })
}

describe("ModifiersPage", () => {
  beforeEach(() => {
    get.mockReset()
    del.mockReset()
    push.mockReset()
    toastError.mockReset()
    toastSuccess.mockReset()
    mockRoutes()
  })

  it("renders each group's rule text and option/product counts", async () => {
    render(<ModifiersPage />, { wrapper: Wrapper })

    expect(await screen.findByText("Boy Seçimi")).toBeInTheDocument()
    expect(screen.getByText("Tek seçim · Zorunlu")).toBeInTheDocument()
    expect(screen.getByText("Ek Malzeme")).toBeInTheDocument()
    expect(screen.getByText("Birden fazla · en fazla 3")).toBeInTheDocument()

    const singleRow = screen.getByText("Boy Seçimi").closest("tr")
    expect(within(singleRow!).getByText("2")).toBeInTheDocument()
    expect(within(singleRow!).getByText("3")).toBeInTheDocument()

    const multiRow = screen.getByText("Ek Malzeme").closest("tr")
    expect(within(multiRow!).getByText("4")).toBeInTheDocument()
    expect(within(multiRow!).getByText("0")).toBeInTheDocument()
  })

  it("navigates to the new-group route", async () => {
    render(<ModifiersPage />, { wrapper: Wrapper })
    await screen.findByText("Boy Seçimi")

    fireEvent.click(screen.getByRole("button", { name: "Grup ekle" }))

    expect(push).toHaveBeenCalledWith("/catalog/modifiers/new")
  })

  it("links a group's name to its detail route", async () => {
    render(<ModifiersPage />, { wrapper: Wrapper })

    expect(await screen.findByRole("link", { name: "Boy Seçimi" })).toHaveAttribute(
      "href",
      "/catalog/modifiers/g1",
    )
  })

  it("shows the group's own product count in the delete confirmation body", async () => {
    del.mockResolvedValue({})
    render(<ModifiersPage />, { wrapper: Wrapper })
    await screen.findByText("Boy Seçimi")

    openRowMenu("Boy Seçimi için işlemler")
    fireEvent.click(await screen.findByRole("menuitem", { name: "Sil" }))

    const dialog = await screen.findByRole("alertdialog")
    expect(within(dialog).getByText("3 üründen kaldırılır, bu işlem geri alınamaz.")).toBeInTheDocument()

    fireEvent.click(within(dialog).getByRole("button", { name: "Sil" }))

    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/v1/catalog/modifier-groups/g1"))
  })
})
