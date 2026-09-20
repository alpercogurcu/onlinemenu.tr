// The product editor's "Bu ürünün N şubede farklı fiyatı/durumu var → Şube
// Fiyatları" note (ADR-DATA-009). Manager-only: any other role would just
// collect a 403 per branch, so for them nothing may be requested at all.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { ProductEditor } from "@/components/catalog/product-editor"
import { clearAccessToken, setAccessToken } from "@/lib/api"
import messages from "@/messages/tr.json"
import { useAuthStore } from "@/store/auth-store"
import type { BranchProductOverride, Product } from "@/types"

global.ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
}
Element.prototype.scrollIntoView = vi.fn()

const PRODUCT_ID = "bbbbbbbb-0000-0000-0000-000000000001"
const PRODUCT: Product = {
  id: PRODUCT_ID,
  tenant_id: "t1",
  branch_id: null,
  category_id: null,
  name: "Adana Kebap",
  description: "",
  image_key: "",
  price_amount: 32_000,
  currency: "TRY",
  sku: "",
  unit: "adet",
  tax_rate_bps: 1000,
  is_active: true,
  sort_order: 0,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
}

const row = (branchId: string, patch: Partial<BranchProductOverride>): BranchProductOverride => ({
  branch_id: branchId,
  product_id: PRODUCT_ID,
  is_available: true,
  price_amount: null,
  updated_at: "2026-09-20T10:00:00Z",
  ...patch,
})

const get = vi.fn()
vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>()
  return { ...actual, default: { get: (...args: unknown[]) => get(...args) } }
})

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => `/catalog/products/${PRODUCT_ID}`,
}))
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn() }) }))

const ROLE_MANAGER = "00000001-0000-0000-0000-000000000006"
const ROLE_CASHIER = "00000001-0000-0000-0000-000000000001"

function signIn(roleId: string) {
  const enc = (obj: unknown) => Buffer.from(JSON.stringify(obj)).toString("base64url")
  setAccessToken(`${enc({ alg: "HS256", typ: "CTX" })}.${enc({ rids: [roleId] })}.sig`)
  useAuthStore.setState({ user: { id: "u1", name: "U", email: "u@x" }, tenantId: "t1" })
}

function Wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <NextIntlClientProvider locale="tr" messages={messages}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </NextIntlClientProvider>
  )
}

beforeEach(() => {
  get.mockReset()
  get.mockImplementation((url: string) => {
    if (url === `/api/v1/catalog/products/${PRODUCT_ID}`) return Promise.resolve({ data: PRODUCT })
    if (url === "/tenants/t1/branches/")
      return Promise.resolve({
        data: [
          { id: "b1", tenant_id: "t1", name: "Ana Şube", is_active: true },
          { id: "b2", tenant_id: "t1", name: "İzmit", is_active: true },
          { id: "b3", tenant_id: "t1", name: "Kırkpınar", is_active: true },
        ],
      })
    // b1: an empty row (available, no price) — reads like no override.
    if (url === "/api/v1/catalog/branches/b1/product-overrides") return Promise.resolve({ data: [row("b1", {})] })
    if (url === "/api/v1/catalog/branches/b2/product-overrides")
      return Promise.resolve({ data: [row("b2", { price_amount: 35_000 })] })
    if (url === "/api/v1/catalog/branches/b3/product-overrides")
      return Promise.resolve({ data: [row("b3", { is_available: false })] })
    return Promise.resolve({ data: [] })
  })
})

afterEach(() => {
  clearAccessToken()
  useAuthStore.setState({ user: null, tenantId: null })
})

describe("ProductEditor branch override note", () => {
  it("tells the manager how many branches differ and links to the pricing screen for the first of them", async () => {
    signIn(ROLE_MANAGER)
    render(<ProductEditor productId={PRODUCT_ID} />, { wrapper: Wrapper })

    expect(await screen.findByText(/Bu ürünün 2 şubede farklı fiyatı\/durumu var/)).toBeInTheDocument()
    const link = screen.getByRole("link", { name: "Şube Fiyatları" })
    expect(link).toHaveAttribute("href", "/catalog/branch-pricing?branch=b2&q=Adana+Kebap")
  })

  it("stays silent for a branch-scoped role and never asks for any override list", async () => {
    signIn(ROLE_CASHIER)
    render(<ProductEditor productId={PRODUCT_ID} />, { wrapper: Wrapper })

    await screen.findByRole("heading", { name: "Adana Kebap" })
    await waitFor(() => expect(get).toHaveBeenCalledWith(`/api/v1/catalog/products/${PRODUCT_ID}`))
    expect(screen.queryByText(/şubede farklı fiyatı/)).not.toBeInTheDocument()
    expect(get.mock.calls.some(([url]) => String(url).includes("product-overrides"))).toBe(false)
    expect(get.mock.calls.some(([url]) => String(url).startsWith("/tenants/"))).toBe(false)
  })
})
