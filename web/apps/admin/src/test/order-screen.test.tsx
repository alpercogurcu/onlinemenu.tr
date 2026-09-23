// Web order screen (/pos/order): tile -> cart, option panel fast path and
// required-group guard, the send body/headers, and the network-error retry
// that must reuse the Idempotency-Key and must not reopen the check.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { AxiosError, AxiosHeaders, type AxiosResponse } from "axios"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { OptionPanel } from "@/components/pos/order/option-panel"
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

const BRANCH = "b1"
const TABLE = { id: "t1", branch_id: BRANCH, zone_id: "z1", name: "Masa 4", capacity: 4, status: "empty", layout_position: null, is_active: true, active_check_id: null }
const stamp = { tenant_id: "tn", created_at: "", updated_at: "" }
const product = (id: string, name: string, price: number, sort: number) => ({
  ...stamp, id, name, price_amount: price, category_id: "cat", branch_id: null, description: "", image_key: "",
  currency: "TRY", sku: "", unit: "adet", tax_rate_bps: 1000, is_active: true, sort_order: sort,
})

function routeGet(url: string) {
  const map: Record<string, unknown> = {
    "/api/v1/pos/tables": [{ zone_id: "z1", zone_name: "Salon", floor: 0, tables: [TABLE] }],
    "/api/v1/catalog/categories": [{ ...stamp, id: "cat", name: "Ana Yemekler", description: "", sort_order: 1, is_active: true }],
    "/api/v1/catalog/products": [product("kebap", "Adana Kebap", 32_000, 1), product("ayran", "Ayran", 4_000, 2)],
    "/api/v1/catalog/products/modifier-groups": [
      {
        product_id: "kebap",
        groups: [
          {
            id: "cook", name: "Pişirme", selection_type: "single", min_selections: 1, max_selections: 1, is_required: true, sort_order: 1,
            modifiers: [
              { id: "az", name: "Az pişmiş", price_delta: 0, sort_order: 1 },
              { id: "orta", name: "Orta", price_delta: 0, sort_order: 2 },
            ],
          },
          {
            id: "extra", name: "Ekstralar", selection_type: "multiple", min_selections: 0, max_selections: null, is_required: false, sort_order: 2,
            modifiers: [{ id: "sos", name: "Ekstra sos", price_delta: 1_500, sort_order: 1 }],
          },
        ],
      },
    ],
  }
  if (!(url in map)) return Promise.reject(new Error(`unexpected GET ${url}`))
  return Promise.resolve({ data: map[url] })
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

function networkError() {
  return new AxiosError("Network Error", "ERR_NETWORK", { headers: new AxiosHeaders() }, {})
}

function httpError(status: number, data: unknown) {
  const config = { headers: new AxiosHeaders() }
  return new AxiosError("x", "ERR_BAD_REQUEST", config, {}, { status, data, statusText: "", headers: {}, config } as AxiosResponse)
}

// Tile names are "<product>, <price> — sepete ekle"; anchored so the cart
// line's "Ayran adedini artır" etc. never match too.
async function tile(product: string) {
  return screen.findByRole("button", { name: new RegExp(`^${product},`) })
}

beforeEach(() => {
  get.mockReset().mockImplementation((url: string) => routeGet(url))
  post.mockReset()
})

// Radix portals + TanStack Query resolution: the first render in a cold
// worker can take several seconds on a loaded machine.
describe("OrderScreen", { timeout: 20_000 }, () => {
  it("one tap adds an option-less product; the sticky bar shows count and total", async () => {
    render(wrap(<OrderScreen branchId={BRANCH} tableId="t1" onBackToTables={vi.fn()} />))
    fireEvent.click(await tile("Ayran"))
    await waitFor(() => expect(screen.getByTestId("cart-total")).toHaveTextContent("40,00"))
    fireEvent.click(await tile("Ayran"))
    await waitFor(() => expect(screen.getByTestId("cart-total")).toHaveTextContent("80,00"))
    expect(screen.getByTestId("cart-count")).toHaveTextContent("1 kalem")
    // products and options are always read with the TABLE's branch (manager = chain-wide)
    expect(get).toHaveBeenCalledWith("/api/v1/catalog/products", { params: { branch_id: BRANCH } })
    expect(get).toHaveBeenCalledWith("/api/v1/catalog/products/modifier-groups", { params: { branch_id: BRANCH } })
    // Opening the screen costs a fixed number of requests, not one per product.
    expect(get.mock.calls.map(([u]) => u).sort()).toEqual([
      "/api/v1/catalog/categories",
      "/api/v1/catalog/products",
      "/api/v1/catalog/products/modifier-groups",
      "/api/v1/pos/tables",
    ])
  })

  it("an option product opens the panel on the fast path; live price; send body + key", async () => {
    post.mockImplementation((url: string) =>
      url === "/api/v1/pos/checks" ? Promise.resolve({ data: { id: "chk1" } }) : Promise.resolve({ data: { id: "o1" } }),
    )
    render(wrap(<OrderScreen branchId={BRANCH} tableId="t1" onBackToTables={vi.fn()} />))

    fireEvent.click(await tile("Adana Kebap"))
    const panel = await screen.findByRole("dialog")
    expect(within(panel).getByRole("radio", { name: /Az pişmiş/ })).toHaveAttribute("aria-checked", "true")
    expect(within(panel).getByTestId("option-line-total")).toHaveTextContent("320,00")
    fireEvent.click(within(panel).getByRole("button", { name: /Ekstra sos/ }))
    fireEvent.click(within(panel).getByRole("button", { name: "Adedi artır" }))
    expect(within(panel).getByTestId("option-line-total")).toHaveTextContent("670,00")
    fireEvent.click(within(panel).getByRole("button", { name: "Sepete ekle" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())

    fireEvent.click(await tile("Ayran"))
    await waitFor(() => expect(screen.getByTestId("cart-count")).toHaveTextContent("2 kalem"))

    fireEvent.click(screen.getAllByRole("button", { name: /Mutfağa gönder/ })[0])
    await screen.findByText("Mutfağa gönderildi")

    expect(post).toHaveBeenNthCalledWith(1, "/api/v1/pos/checks", { branch_id: BRANCH, table_id: "t1", table_label: "Masa 4" })
    const [url, body, config] = post.mock.calls[1]
    expect(url).toBe("/api/v1/pos/orders")
    expect(config.headers["Idempotency-Key"]).toMatch(/^[0-9a-f-]{36}$/)
    expect(body).toMatchObject({ branch_id: BRANCH, check_id: "chk1", order_channel: "dine_in" })
    expect(body.items).toEqual([
      expect.objectContaining({ product_id: "kebap", quantity: 2, unit_price_amount: 33_500, modifier_ids: ["az", "sos"] }),
      expect.objectContaining({ product_id: "ayran", quantity: 1, unit_price_amount: 4_000, modifier_ids: [] }),
    ])
    expect(screen.getByTestId("cart-count")).toHaveTextContent("0 kalem")
  })

  it("network error keeps the cart; Tekrar dene resends the SAME key without reopening the check", async () => {
    let orderCalls = 0
    post.mockImplementation((url: string) => {
      if (url === "/api/v1/pos/checks") return Promise.resolve({ data: { id: "chk1" } })
      orderCalls += 1
      return orderCalls === 1 ? Promise.reject(networkError()) : Promise.resolve({ data: { id: "o1" } })
    })
    render(wrap(<OrderScreen branchId={BRANCH} tableId="t1" onBackToTables={vi.fn()} />))
    fireEvent.click(await tile("Ayran"))
    await waitFor(() => expect(screen.getByTestId("cart-count")).toHaveTextContent("1 kalem"))
    fireEvent.click(screen.getAllByRole("button", { name: /Mutfağa gönder/ })[0])

    expect((await screen.findAllByRole("alert"))[0]).toHaveTextContent(/Bağlantı kurulamadı/)
    expect(screen.getByTestId("cart-count")).toHaveTextContent("1 kalem")

    fireEvent.click(screen.getAllByRole("button", { name: "Tekrar dene" })[0])
    await screen.findByText("Mutfağa gönderildi")

    const orderPosts = post.mock.calls.filter(([u]) => u === "/api/v1/pos/orders")
    expect(orderPosts).toHaveLength(2)
    expect(orderPosts[1][2].headers["Idempotency-Key"]).toBe(orderPosts[0][2].headers["Idempotency-Key"])
    expect(orderPosts[1][1]).toEqual(orderPosts[0][1])
    expect(post.mock.calls.filter(([u]) => u === "/api/v1/pos/checks")).toHaveLength(1)
  })

  it("price_mismatch -> Turkish message, cart kept, catalog refetched", async () => {
    post.mockImplementation((url: string) =>
      url === "/api/v1/pos/checks"
        ? Promise.resolve({ data: { id: "chk1" } })
        : Promise.reject(httpError(422, { error: "price mismatch", code: "price_mismatch" })),
    )
    render(wrap(<OrderScreen branchId={BRANCH} tableId="t1" onBackToTables={vi.fn()} />))
    fireEvent.click(await tile("Ayran"))
    await waitFor(() => expect(screen.getByTestId("cart-count")).toHaveTextContent("1 kalem"))
    const productCalls = () => get.mock.calls.filter(([u]) => u === "/api/v1/catalog/products").length
    const before = productCalls()
    fireEvent.click(screen.getAllByRole("button", { name: /Mutfağa gönder/ })[0])
    expect((await screen.findAllByRole("alert"))[0]).toHaveTextContent(/Fiyat değişmiş/)
    expect(screen.queryAllByRole("button", { name: "Tekrar dene" })).toHaveLength(0)
    expect(screen.getByTestId("cart-count")).toHaveTextContent("1 kalem")
    await waitFor(() => expect(productCalls()).toBeGreaterThan(before))
  })
})

describe("OptionPanel", () => {
  const kebap = { id: "k", name: "Kebap", price_amount: 10_000, currency: "TRY", tax_rate_bps: 1000, unit: "adet" }
  const sauces = {
    id: "sauce", name: "Sos", selection_type: "multiple", min_selections: 1, max_selections: 0, is_required: true,
    modifiers: [{ id: "aci", name: "Acı sos", price_delta: 0 }, { id: "sar", name: "Sarımsaklı", price_delta: 500 }],
  }

  it("an unanswered required group blocks the add and says why", () => {
    const onConfirm = vi.fn()
    render(wrap(<OptionPanel product={kebap} groups={[sauces]} onConfirm={onConfirm} onCancel={vi.fn()} />))
    const first = screen.getByRole("button", { name: /Acı sos/ })
    expect(first).toHaveAttribute("aria-pressed", "true")
    fireEvent.click(first)
    fireEvent.click(screen.getByRole("button", { name: "Sepete ekle" }))
    expect(onConfirm).not.toHaveBeenCalled()
    expect(screen.getByRole("alert")).toHaveTextContent("Bu gruptan bir seçim yapın.")

    fireEvent.click(screen.getByRole("button", { name: /Sarımsaklı/ }))
    fireEvent.click(screen.getByRole("button", { name: "Soğansız" }))
    fireEvent.click(screen.getByRole("button", { name: "Sepete ekle" }))
    expect(onConfirm).toHaveBeenCalledWith({
      modifiers: [{ id: "sar", groupId: "sauce", name: "Sarımsaklı", priceDelta: 500 }],
      note: "Soğansız",
      quantity: 1,
    })
  })
})
