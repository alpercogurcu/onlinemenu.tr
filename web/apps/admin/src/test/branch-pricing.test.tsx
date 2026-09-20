// Behaviour of the owner-facing "Şube Fiyatları" screen (ADR-DATA-009): the
// list is the TENANT catalog joined with the selected branch's overrides,
// every edit is one request per row, and the request always carries BOTH
// fields (the backend defaults an omitted is_available to true, so a
// price-only body would silently re-open a product the owner closed).
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import BranchPricingPage from "@/app/(main)/catalog/branch-pricing/page"
import { BranchPricing } from "@/components/catalog/branch-pricing"
import { clearAccessToken, setAccessToken } from "@/lib/api"
import messages from "@/messages/tr.json"
import { useAuthStore } from "@/store/auth-store"
import type { BranchProductOverride, Product } from "@/types"

const replace = vi.fn()
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace }),
  usePathname: () => "/catalog/branch-pricing",
  useSearchParams: () => new URLSearchParams(),
}))

const get = vi.fn()
const put = vi.fn()
const del = vi.fn()
vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>()
  return {
    ...actual,
    default: {
      get: (...args: unknown[]) => get(...args),
      put: (...args: unknown[]) => put(...args),
      delete: (...args: unknown[]) => del(...args),
    },
  }
})

const toastError = vi.fn()
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: (...args: unknown[]) => toastError(...args) },
}))

const product = (id: string, name: string, price: number, patch: Partial<Product> = {}): Product => ({
  id,
  tenant_id: "t1",
  branch_id: null,
  category_id: "c1",
  name,
  description: "",
  image_key: "",
  price_amount: price,
  currency: "TRY",
  sku: "",
  unit: "adet",
  tax_rate_bps: 1000,
  is_active: true,
  sort_order: 0,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  ...patch,
})

const PRODUCTS = [
  product("p1", "Adana Kebap", 32_000),
  product("p2", "Tavuk Şiş", 28_000),
  product("p3", "Lahmacun", 9_000),
]

const override = (productId: string, patch: Partial<BranchProductOverride> = {}): BranchProductOverride => ({
  branch_id: "b2",
  product_id: productId,
  is_available: true,
  price_amount: null,
  updated_at: "2026-09-20T10:00:00Z",
  ...patch,
})

let overrides: BranchProductOverride[] = []

function mockRoutes() {
  get.mockImplementation((url: string) => {
    if (url === "/tenants/t1/branches/")
      return Promise.resolve({
        data: [
          { id: "b1", tenant_id: "t1", name: "Ana Şube", is_active: true },
          { id: "b2", tenant_id: "t1", name: "İzmit Şube", is_active: true },
        ],
      })
    if (url === "/api/v1/catalog/products") return Promise.resolve({ data: PRODUCTS })
    if (url === "/api/v1/catalog/categories")
      return Promise.resolve({ data: [{ id: "c1", tenant_id: "t1", name: "Ana Yemek", description: "", sort_order: 0, is_active: true, created_at: "", updated_at: "" }] })
    if (url === "/api/v1/catalog/branches/b1/product-overrides") return Promise.resolve({ data: [] })
    if (url === "/api/v1/catalog/branches/b2/product-overrides") return Promise.resolve({ data: overrides })
    return Promise.resolve({ data: [] })
  })
  // A stateful fake server: the hook refetches after every save, so a static
  // list would overwrite the very state these tests assert on.
  put.mockImplementation((url: string, body: { is_available: boolean; price_amount: number | null }) => {
    const productId = /products\/([^/]+)\/override$/.exec(url)?.[1] ?? ""
    const saved = override(productId, body)
    overrides = [...overrides.filter((o) => o.product_id !== productId), saved]
    return Promise.resolve({ data: saved })
  })
  del.mockImplementation((url: string) => {
    const productId = /products\/([^/]+)\/override$/.exec(url)?.[1] ?? ""
    overrides = overrides.filter((o) => o.product_id !== productId)
    return Promise.resolve({ status: 204 })
  })
}

function Wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return (
    <NextIntlClientProvider locale="tr" messages={messages}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </NextIntlClientProvider>
  )
}

const rowOf = (id: string) => screen.getByTestId(`branch-pricing-row-${id}`)
const priceInput = (id: string) => within(rowOf(id)).getByLabelText(/şube fiyatı$/) as HTMLInputElement

async function openIzmit() {
  render(<BranchPricing />, { wrapper: Wrapper })
  await screen.findByText("Adana Kebap")
  fireEvent.change(screen.getByLabelText("Şube"), { target: { value: "b2" } })
  await waitFor(() => expect(get).toHaveBeenCalledWith("/api/v1/catalog/branches/b2/product-overrides"))
  // The table is replaced by a skeleton while the new branch's overrides load.
  await screen.findByTestId("branch-pricing-row-p1")
}

function commitPrice(id: string, text: string) {
  const input = priceInput(id)
  fireEvent.change(input, { target: { value: text } })
  fireEvent.blur(input)
}

beforeEach(() => {
  useAuthStore.setState({ tenantId: "t1" })
  overrides = []
  get.mockReset()
  put.mockReset()
  del.mockReset()
  toastError.mockReset()
  replace.mockReset()
  mockRoutes()
})

describe("BranchPricing", () => {
  it("lists the whole tenant catalog with tenant prices and 'Genel fiyat' badges when nothing is overridden", async () => {
    render(<BranchPricing />, { wrapper: Wrapper })

    expect(await screen.findByText("Adana Kebap")).toBeInTheDocument()
    expect(screen.getByText("Tavuk Şiş")).toBeInTheDocument()
    expect(within(rowOf("p1")).getByText("₺320,00")).toBeInTheDocument()
    expect(within(rowOf("p1")).getByText("Genel fiyat", { selector: "span[data-slot=badge]" })).toBeInTheDocument()
    expect(screen.getByText("3 ürün · 0 şubeye özel")).toBeInTheDocument()
    // Blank = tenant price: the tenant price shows as the placeholder.
    expect(priceInput("p1").value).toBe("")
    expect(priceInput("p1").placeholder).toBe("320,00")
  })

  it("shows a branch price and a closed product — a closed product must stay listed so it can be re-opened", async () => {
    overrides = [override("p2", { price_amount: 31_000 }), override("p3", { is_available: false })]
    await openIzmit()

    await waitFor(() => expect(priceInput("p2").value).toBe("310,00"))
    expect(within(rowOf("p2")).getByText("Şube fiyatı", { selector: "span[data-slot=badge]" })).toBeInTheDocument()
    expect(within(rowOf("p3")).getByText("Kapalı")).toBeInTheDocument()
    expect(within(rowOf("p3")).getByRole("switch")).not.toBeChecked()
    expect(within(rowOf("p1")).getByRole("switch")).toBeChecked()
    expect(screen.getByText("3 ürün · 2 şubeye özel")).toBeInTheDocument()
  })

  it("commits a typed price on blur: one PUT carrying the lira text as kuruş AND the current availability", async () => {
    await openIzmit()

    commitPrice("p1", "310,50")

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/v1/catalog/branches/b2/products/p1/override", {
        is_available: true,
        price_amount: 31_050,
      }),
    )
    expect(put).toHaveBeenCalledTimes(1)
    await waitFor(() =>
      expect(within(rowOf("p1")).getByText("Şube fiyatı", { selector: "span[data-slot=badge]" })).toBeInTheDocument(),
    )
  })

  it("commits on Enter", async () => {
    await openIzmit()
    const input = priceInput("p1")
    input.focus()
    fireEvent.change(input, { target: { value: "300" } })
    fireEvent.keyDown(input, { key: "Enter" })

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put.mock.calls[0][1]).toEqual({ is_available: true, price_amount: 30_000 })
  })

  it("does not re-open a closed product when only its price is edited", async () => {
    overrides = [override("p3", { is_available: false })]
    await openIzmit()
    await waitFor(() => expect(within(rowOf("p3")).getByRole("switch")).not.toBeChecked())

    commitPrice("p3", "85")

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put.mock.calls[0][1]).toEqual({ is_available: false, price_amount: 8_500 })
  })

  it("keeps the branch price when the switch closes a product", async () => {
    overrides = [override("p2", { price_amount: 31_000 })]
    await openIzmit()
    await waitFor(() => expect(priceInput("p2").value).toBe("310,00"))

    fireEvent.click(within(rowOf("p2")).getByRole("switch"))

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put.mock.calls[0]).toEqual([
      "/api/v1/catalog/branches/b2/products/p2/override",
      { is_available: false, price_amount: 31_000 },
    ])
    await waitFor(() => expect(within(rowOf("p2")).getByText("Kapalı")).toBeInTheDocument())
  })

  it("clearing the price PUTs an empty price (keeps availability) — it is not 'Varsayılana dön'", async () => {
    overrides = [override("p2", { price_amount: 31_000 })]
    await openIzmit()
    await waitFor(() => expect(priceInput("p2").value).toBe("310,00"))

    commitPrice("p2", "")

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put.mock.calls[0][1]).toEqual({ is_available: true, price_amount: null })
    expect(del).not.toHaveBeenCalled()
  })

  it("never sends unparseable text: it reverts the field and toasts instead", async () => {
    overrides = [override("p2", { price_amount: 31_000 })]
    await openIzmit()
    await waitFor(() => expect(priceInput("p2").value).toBe("310,00"))

    fireEvent.change(priceInput("p2"), { target: { value: "abc" } })
    expect(priceInput("p2")).toHaveAttribute("aria-invalid", "true")
    fireEvent.blur(priceInput("p2"))

    expect(put).not.toHaveBeenCalled()
    expect(toastError).toHaveBeenCalledWith("Geçerli bir tutar girin (örn. 125,50)")
    expect(priceInput("p2").value).toBe("310,00")
  })

  it("sends nothing when the price did not change", async () => {
    overrides = [override("p2", { price_amount: 31_000 })]
    await openIzmit()
    await waitFor(() => expect(priceInput("p2").value).toBe("310,00"))

    commitPrice("p2", "310")

    expect(put).not.toHaveBeenCalled()
    expect(priceInput("p2").value).toBe("310,00")
  })

  it("'Varsayılana dön' DELETEs the row and the product falls back to the tenant price", async () => {
    overrides = [override("p2", { price_amount: 31_000, is_available: false })]
    await openIzmit()
    await waitFor(() => expect(within(rowOf("p2")).getByText("Kapalı")).toBeInTheDocument())

    fireEvent.click(within(rowOf("p2")).getByRole("button", { name: "Tavuk Şiş için varsayılana dön" }))

    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/v1/catalog/branches/b2/products/p2/override"))
    await waitFor(() =>
      expect(within(rowOf("p2")).getByText("Genel fiyat", { selector: "span[data-slot=badge]" })).toBeInTheDocument(),
    )
    expect(within(rowOf("p2")).getByRole("switch")).toBeChecked()
    expect(priceInput("p2").value).toBe("")
  })

  it("disables 'Varsayılana dön' for a product that has nothing to reset", async () => {
    await openIzmit()
    expect(within(rowOf("p1")).getByRole("button", { name: /varsayılana dön/ })).toBeDisabled()
  })

  it("rolls the row back and toasts when the save fails", async () => {
    put.mockRejectedValue(new Error("boom"))
    overrides = [override("p2", { price_amount: 31_000 })]
    await openIzmit()
    await waitFor(() => expect(priceInput("p2").value).toBe("310,00"))

    commitPrice("p2", "290")

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("İşlem tamamlanamadı, değişiklik geri alındı"))
    await waitFor(() => expect(priceInput("p2").value).toBe("310,00"))
  })

  it("'Yalnız şubeye özel olanlar' narrows to differing products; an empty-row product does not count", async () => {
    overrides = [override("p2", { price_amount: 31_000 }), override("p3")]
    await openIzmit()
    await waitFor(() => expect(priceInput("p2").value).toBe("310,00"))

    fireEvent.click(screen.getByRole("button", { name: "Yalnız şubeye özel olanlar" }))

    expect(screen.getByRole("button", { name: "Yalnız şubeye özel olanlar" })).toHaveAttribute("aria-pressed", "true")
    expect(screen.getByText("Tavuk Şiş")).toBeInTheDocument()
    expect(screen.queryByText("Adana Kebap")).not.toBeInTheDocument()
    expect(screen.queryByText("Lahmacun")).not.toBeInTheDocument()
  })

  it("says every product sells at the tenant price when the override filter finds nothing", async () => {
    await openIzmit()

    fireEvent.click(screen.getByRole("button", { name: "Yalnız şubeye özel olanlar" }))

    expect(screen.getByText("Bu şubede tüm ürünler genel fiyatla satılıyor")).toBeInTheDocument()
  })

  it("filters by search text", async () => {
    render(<BranchPricing />, { wrapper: Wrapper })
    await screen.findByText("Adana Kebap")

    fireEvent.change(screen.getByPlaceholderText("Ürün ara"), { target: { value: "lahm" } })

    expect(screen.getByText("Lahmacun")).toBeInTheDocument()
    expect(screen.queryByText("Adana Kebap")).not.toBeInTheDocument()

    fireEvent.change(screen.getByPlaceholderText("Ürün ara"), { target: { value: "zzz" } })
    expect(screen.getByText("Aramanızla eşleşen ürün yok")).toBeInTheDocument()
  })

  it("switching branch loads that branch's overrides", async () => {
    overrides = [override("p2", { price_amount: 31_000 })]
    render(<BranchPricing />, { wrapper: Wrapper })
    await screen.findByText("Adana Kebap")
    expect(priceInput("p2").value).toBe("")

    fireEvent.change(screen.getByLabelText("Şube"), { target: { value: "b2" } })

    await waitFor(() => expect(priceInput("p2").value).toBe("310,00"))
    expect(replace).toHaveBeenCalledWith("/catalog/branch-pricing?branch=b2")
  })
})

describe("BranchPricingPage (manager-only guard)", () => {
  const ROLE_MANAGER = "00000001-0000-0000-0000-000000000006"
  const ROLE_CASHIER = "00000001-0000-0000-0000-000000000001"

  function ctxToken(roleId: string) {
    const enc = (obj: unknown) => Buffer.from(JSON.stringify(obj)).toString("base64url")
    return `${enc({ alg: "HS256", typ: "CTX" })}.${enc({ rids: [roleId] })}.sig`
  }

  function signIn(roleId: string) {
    setAccessToken(ctxToken(roleId))
    useAuthStore.setState({ user: { id: "u1", name: "U", email: "u@x" }, tenantId: "t1" })
  }

  afterEach(() => {
    clearAccessToken()
    useAuthStore.setState({ user: null, tenantId: null })
  })

  it("renders the screen for the manager", async () => {
    signIn(ROLE_MANAGER)
    render(<BranchPricingPage />, { wrapper: Wrapper })

    expect(await screen.findByText("Adana Kebap")).toBeInTheDocument()
  })

  it("shows an access notice — and fires no request — for any other role", () => {
    signIn(ROLE_CASHIER)
    render(<BranchPricingPage />, { wrapper: Wrapper })

    expect(screen.getByText("Bu sayfaya erişim yetkiniz yok")).toBeInTheDocument()
    expect(get).not.toHaveBeenCalled()
  })
})
