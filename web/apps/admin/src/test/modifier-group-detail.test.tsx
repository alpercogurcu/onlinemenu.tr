// Component tests for the seçenek grubu detail editor: the "Kural" card's
// selection-type segmented control (En fazla field visibility + the
// max_selections:1 forced onto a "single" save), the options card's inline
// row editing (new-row Enter -> POST, allowNegative price delta -> PUT, the
// move-down button's two-PUT sort_order swap), and the "kullanan ürünler"
// card resolving product ids to names. The preview's active-only filtering is
// covered directly against ModifierPreview (no api/router wiring needed for
// that one).
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { ModifierGroupEditor } from "@/components/catalog/modifier-group-editor"
import { ModifierPreview } from "@/components/catalog/modifier-preview"
import messages from "@/messages/tr.json"
import type { Modifier, ModifierGroup, Product } from "@/types"

const push = vi.fn()
const replace = vi.fn()
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push, replace }),
  // useBreadcrumbLabel (wired into ModifierGroupEditor) reads this — it
  // doesn't affect any assertion here since no DynamicBreadcrumb is mounted
  // in these tests, but the hook throws without a usePathname to call.
  usePathname: () => "/catalog/modifiers/g1",
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

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

const GROUP: ModifierGroup = {
  id: "g1",
  tenant_id: "t1",
  name: "Ek Malzeme",
  selection_type: "multiple",
  min_selections: 0,
  max_selections: 3,
  is_required: false,
  sort_order: 0,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
}

function makeModifier(overrides: Partial<Modifier>): Modifier {
  return {
    id: "m1",
    tenant_id: "t1",
    group_id: "g1",
    name: "Peynir",
    price_delta: 0,
    is_active: true,
    sort_order: 10,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  }
}

function makeProduct(overrides: Partial<Product>): Product {
  return {
    id: "prod-1",
    tenant_id: "t1",
    branch_id: null,
    category_id: null,
    name: "Cheeseburger",
    description: "",
    image_key: "",
    price_amount: 12000,
    currency: "TRY",
    sku: "",
    unit: "adet",
    tax_rate_bps: 1000,
    is_active: true,
    sort_order: 0,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  }
}

let modifiersForG1: Modifier[] = []
let productIdsForG1: string[] = []
let productsList: Product[] = []

function mockRoutes() {
  get.mockImplementation((url: string) => {
    if (url === "/api/v1/catalog/modifier-groups/g1") return Promise.resolve({ data: GROUP })
    if (url === "/api/v1/catalog/modifier-groups/g1/modifiers") {
      return Promise.resolve({ data: modifiersForG1 })
    }
    if (url === "/api/v1/catalog/modifier-groups/g1/products") {
      return Promise.resolve({ data: productIdsForG1 })
    }
    if (url === "/api/v1/catalog/products") return Promise.resolve({ data: productsList })
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

describe("ModifierGroupEditor — Kural card", () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    put.mockReset()
    push.mockReset()
    replace.mockReset()
    modifiersForG1 = []
    productIdsForG1 = []
    productsList = []
    mockRoutes()
    post.mockResolvedValue({ data: { ...GROUP, id: "new-g1" } })
  })

  it("shows En fazla only for Birden fazla, and forces max_selections:1 when saved as Tek seçim", async () => {
    render(<ModifierGroupEditor groupId={null} />, { wrapper: Wrapper })

    expect(screen.queryByLabelText("En fazla")).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Birden fazla" }))
    expect(screen.getByLabelText("En fazla")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Tek seçim" }))
    expect(screen.queryByLabelText("En fazla")).not.toBeInTheDocument()

    fireEvent.change(screen.getByLabelText(/Grup adı/), { target: { value: "Boy Seçimi" } })
    fireEvent.click(screen.getByRole("button", { name: "Kaydet" }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith(
      "/api/v1/catalog/modifier-groups",
      expect.objectContaining({
        name: "Boy Seçimi",
        selection_type: "single",
        max_selections: 1,
        min_selections: 0,
        is_required: false,
      }),
    )
  })

  it("saves an existing group with the full body, including sort_order", async () => {
    // Backend PUT REPLACES the whole row — omitting sort_order used to reset
    // it to 0 on every save (I1 in the katalog-ux final review).
    put.mockResolvedValue({ data: GROUP })
    render(<ModifierGroupEditor groupId="g1" />, { wrapper: Wrapper })
    await screen.findByDisplayValue(GROUP.name)

    fireEvent.change(screen.getByLabelText(/Grup adı/), { target: { value: "Ek Malzeme (v2)" } })
    fireEvent.click(screen.getByRole("button", { name: "Kaydet" }))

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put).toHaveBeenCalledWith("/api/v1/catalog/modifier-groups/g1", {
      name: "Ek Malzeme (v2)",
      selection_type: GROUP.selection_type,
      max_selections: GROUP.max_selections,
      min_selections: GROUP.min_selections,
      is_required: GROUP.is_required,
      sort_order: GROUP.sort_order,
    })
  })

  it("shows the save-group-first notice on the options card before the group exists", () => {
    render(<ModifierGroupEditor groupId={null} />, { wrapper: Wrapper })

    expect(
      screen.getByText("Seçenek eklemek için önce grubu kaydedin."),
    ).toBeInTheDocument()
  })

  it("accepts max_selections:1 on a required multiple-selection group (max is not < min)", async () => {
    render(<ModifierGroupEditor groupId={null} />, { wrapper: Wrapper })

    fireEvent.click(screen.getByRole("button", { name: "Birden fazla" }))
    fireEvent.change(screen.getByLabelText("En fazla"), { target: { value: "1" } })
    fireEvent.click(screen.getByRole("button", { name: "Evet, en az 1" }))
    fireEvent.change(screen.getByLabelText(/Grup adı/), { target: { value: "Tek Ek" } })
    fireEvent.click(screen.getByRole("button", { name: "Kaydet" }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith(
      "/api/v1/catalog/modifier-groups",
      expect.objectContaining({
        selection_type: "multiple",
        max_selections: 1,
        min_selections: 1,
        is_required: true,
      }),
    )
    expect(
      screen.queryByText("En fazla değeri en az değerinden küçük olamaz"),
    ).not.toBeInTheDocument()
  })

  it("rejects max_selections < min_selections — reachable only via a group already saved that way", async () => {
    // MAX_SELECTION_OPTIONS starts at 1 and min_selections tops out at 1
    // (is_required), so this branch can never be triggered by picking values
    // in the form itself — it's a defensive check against a row whose
    // max_selections arrived from the API already below min_selections
    // (legacy data, or an edit made directly against the backend).
    const badGroup: ModifierGroup = {
      ...GROUP,
      id: "g-bad",
      selection_type: "multiple",
      is_required: true,
      min_selections: 1,
      max_selections: 0,
    }
    get.mockImplementation((url: string) => {
      if (url === "/api/v1/catalog/modifier-groups/g-bad") return Promise.resolve({ data: badGroup })
      if (url === "/api/v1/catalog/modifier-groups/g-bad/modifiers") return Promise.resolve({ data: [] })
      if (url === "/api/v1/catalog/modifier-groups/g-bad/products") return Promise.resolve({ data: [] })
      if (url === "/api/v1/catalog/products") return Promise.resolve({ data: [] })
      return Promise.resolve({ data: [] })
    })

    render(<ModifierGroupEditor groupId="g-bad" />, { wrapper: Wrapper })
    await screen.findByDisplayValue("Ek Malzeme")

    fireEvent.click(screen.getByRole("button", { name: "Kaydet" }))

    expect(
      await screen.findByText("En fazla değeri en az değerinden küçük olamaz"),
    ).toBeInTheDocument()
    expect(put).not.toHaveBeenCalledWith(
      "/api/v1/catalog/modifier-groups/g-bad",
      expect.anything(),
    )
  })
})

describe("ModifierGroupEditor — Seçenekler card", () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    put.mockReset()
    push.mockReset()
    replace.mockReset()
    modifiersForG1 = []
    productIdsForG1 = []
    productsList = []
    mockRoutes()
  })

  it("posts a new row on Enter with sort_order = last + 10", async () => {
    modifiersForG1 = [makeModifier({ id: "m1", name: "Peynir", sort_order: 10 })]
    post.mockResolvedValue({ data: makeModifier({ id: "m2", name: "Büyük", sort_order: 20 }) })

    render(<ModifierGroupEditor groupId="g1" />, { wrapper: Wrapper })
    await screen.findByDisplayValue("Peynir")

    const addRow = screen.getByLabelText("Yeni seçenek…")
    fireEvent.change(addRow, { target: { value: "Büyük" } })
    fireEvent.keyDown(addRow, { key: "Enter" })

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith("/api/v1/catalog/modifier-groups/g1/modifiers", {
      name: "Büyük",
      price_delta: 0,
      is_active: true,
      sort_order: 20,
    })
  })

  it("parses a negative price delta with allowNegative, sending the full row body", async () => {
    modifiersForG1 = [
      makeModifier({ id: "m1", name: "Soğan", price_delta: 0, is_active: true, sort_order: 10 }),
    ]
    put.mockResolvedValue({ data: modifiersForG1[0] })

    render(<ModifierGroupEditor groupId="g1" />, { wrapper: Wrapper })
    await screen.findByDisplayValue("Soğan")

    const priceInput = screen.getByLabelText("Fiyat farkı")
    priceInput.focus()
    fireEvent.change(priceInput, { target: { value: "-5" } })
    fireEvent.keyDown(priceInput, { key: "Enter" })

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put).toHaveBeenCalledWith("/api/v1/catalog/modifier-groups/g1/modifiers/m1", {
      name: "Soğan",
      price_delta: -500,
      is_active: true,
      sort_order: 10,
    })
  })

  it("blurs the price field on Enter so a following Tab doesn't fire a second PUT", async () => {
    modifiersForG1 = [
      makeModifier({ id: "m1", name: "Soğan", price_delta: 0, is_active: true, sort_order: 10 }),
    ]
    put.mockResolvedValue({ data: modifiersForG1[0] })

    render(<ModifierGroupEditor groupId="g1" />, { wrapper: Wrapper })
    await screen.findByDisplayValue("Soğan")

    const priceInput = screen.getByLabelText("Fiyat farkı")
    priceInput.focus()
    fireEvent.change(priceInput, { target: { value: "-5" } })
    fireEvent.keyDown(priceInput, { key: "Enter" })

    // Enter commits by really blurring the field (not by calling the commit
    // function directly), so focus has already moved on by the time Enter's
    // handler returns.
    expect(document.activeElement).not.toBe(priceInput)
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))

    fireEvent.keyDown(priceInput, { key: "Tab" })
    expect(put).toHaveBeenCalledTimes(1)
  })

  it("shows the composed options/products summary line for an existing group", async () => {
    modifiersForG1 = [makeModifier({ id: "m1" }), makeModifier({ id: "m2", sort_order: 20 })]
    productIdsForG1 = ["prod-1", "prod-2", "prod-3"]

    render(<ModifierGroupEditor groupId="g1" />, { wrapper: Wrapper })

    expect(await screen.findByText("2 seçenek · 3 üründe kullanılıyor")).toBeInTheDocument()
  })

  it("swaps sort_order with two full-body PUTs when a row is moved down", async () => {
    modifiersForG1 = [
      makeModifier({ id: "m1", name: "Peynir", price_delta: 0, is_active: true, sort_order: 10 }),
      makeModifier({ id: "m2", name: "Sosis", price_delta: 100, is_active: false, sort_order: 20 }),
    ]
    put.mockResolvedValue({ data: {} })

    render(<ModifierGroupEditor groupId="g1" />, { wrapper: Wrapper })
    await screen.findByDisplayValue("Peynir")

    const moveUpButtons = screen.getAllByRole("button", { name: "Yukarı taşı" })
    const moveDownButtons = screen.getAllByRole("button", { name: "Aşağı taşı" })
    expect(moveUpButtons[0]).toBeDisabled()
    expect(moveUpButtons[0]).toHaveAttribute("aria-disabled", "true")
    expect(moveUpButtons[1]).not.toBeDisabled()
    expect(moveDownButtons[0]).not.toBeDisabled()
    expect(moveDownButtons[1]).toBeDisabled()
    expect(moveDownButtons[1]).toHaveAttribute("aria-disabled", "true")

    fireEvent.click(moveDownButtons[0])

    await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
    expect(put).toHaveBeenCalledWith("/api/v1/catalog/modifier-groups/g1/modifiers/m1", {
      name: "Peynir",
      price_delta: 0,
      is_active: true,
      sort_order: 20,
    })
    expect(put).toHaveBeenCalledWith("/api/v1/catalog/modifier-groups/g1/modifiers/m2", {
      name: "Sosis",
      price_delta: 100,
      is_active: false,
      sort_order: 10,
    })
  })

  it("sends the unchanged name and price when toggling active", async () => {
    modifiersForG1 = [
      makeModifier({ id: "m1", name: "Peynir", price_delta: 250, is_active: true, sort_order: 10 }),
    ]
    put.mockResolvedValue({ data: modifiersForG1[0] })

    render(<ModifierGroupEditor groupId="g1" />, { wrapper: Wrapper })
    await screen.findByDisplayValue("Peynir")

    fireEvent.click(screen.getByLabelText("Satışta"))

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put).toHaveBeenCalledWith("/api/v1/catalog/modifier-groups/g1/modifiers/m1", {
      name: "Peynir",
      price_delta: 250,
      is_active: false,
      sort_order: 10,
    })
  })

  it("keeps the previous name and shows validation instead of sending an empty name", async () => {
    modifiersForG1 = [makeModifier({ id: "m1", name: "Peynir" })]

    render(<ModifierGroupEditor groupId="g1" />, { wrapper: Wrapper })
    const nameInput = await screen.findByDisplayValue("Peynir")

    fireEvent.change(nameInput, { target: { value: "   " } })
    fireEvent.blur(nameInput)

    expect(put).not.toHaveBeenCalled()
    expect(nameInput).toHaveValue("Peynir")
    expect(screen.getByText("Grup adı zorunludur")).toBeInTheDocument()
  })
})

describe("ModifierGroupEditor — Bu grubu kullanan ürünler card", () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    put.mockReset()
    push.mockReset()
    replace.mockReset()
    modifiersForG1 = []
    productIdsForG1 = ["prod-1", "prod-2"]
    productsList = [
      makeProduct({ id: "prod-1", name: "Cheeseburger" }),
      makeProduct({ id: "prod-2", name: "Falafel Wrap" }),
    ]
    mockRoutes()
  })

  it("lists the names of the products using this group", async () => {
    render(<ModifierGroupEditor groupId="g1" />, { wrapper: Wrapper })

    expect(await screen.findByText("Cheeseburger")).toBeInTheDocument()
    expect(screen.getByText("Falafel Wrap")).toBeInTheDocument()
    expect(
      screen.getByText("Buradaki değişiklik 2 üründe de geçerli olur."),
    ).toBeInTheDocument()
  })
})

describe("ModifierPreview", () => {
  it("hides an option that is not on sale", () => {
    render(
      <NextIntlClientProvider locale="tr" messages={messages}>
        <ModifierPreview
          name="Ek Malzeme"
          selectionType="multiple"
          isRequired={false}
          modifiers={[
            makeModifier({ id: "m1", name: "Peynir", is_active: true }),
            makeModifier({ id: "m2", name: "Sosis", is_active: false }),
          ]}
        />
      </NextIntlClientProvider>,
    )

    expect(screen.getByText("Peynir")).toBeInTheDocument()
    expect(screen.queryByText("Sosis")).not.toBeInTheDocument()
  })
})
