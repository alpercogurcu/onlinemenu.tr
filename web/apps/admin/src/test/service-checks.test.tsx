// Table picker's Gel Al / Paket flows: the three segments, customer rows for
// tableless checks, the new-check form (name required, delivery also phone)
// and the exact POST /pos/checks body — no table_id, optional fields only
// when filled.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { TablePicker } from "@/components/pos/order/table-picker"
import messages from "@/messages/tr.json"
import type { Check } from "@/types"

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

const check = (over: Partial<Check>): Check =>
  ({
    id: "c1",
    tenant_id: "tn",
    branch_id: BRANCH,
    table_label: "",
    pax: 0,
    status: "open",
    note: "",
    opened_at: "2026-10-01T10:00:00Z",
    closed_at: null,
    ...over,
  }) as Check

const DINE_IN = check({ id: "din1", table_label: "Masa 4", service_type: "dine_in", total: 12_000 })
const TAKEAWAY = check({ id: "svc1", table_label: "Alper Vural", service_type: "takeaway", customer_name: "Alper Vural", total: 94_000 })
const DELIVERY = check({
  id: "svc2",
  table_label: "Deniz Kaya",
  service_type: "delivery",
  customer_name: "Deniz Kaya",
  customer_phone: "05321234567",
  total: 25_000,
})

function routeGet(url: string) {
  const map: Record<string, unknown> = {
    "/api/v1/pos/tables": [
      {
        zone_id: "z1",
        zone_name: "Salon",
        floor: 0,
        tables: [
          { id: "t1", branch_id: BRANCH, zone_id: "z1", name: "Masa 4", capacity: 4, status: "empty", layout_position: null, is_active: true, active_check_id: null },
        ],
      },
    ],
    "/api/v1/pos/checks": [DINE_IN, TAKEAWAY, DELIVERY],
    "/api/v1/pos/checks/svc1/orders": [
      { id: "o1", check_id: "svc1", status: "pending", items: [{ id: "i1", product_id: "p", product_name: "Adana", quantity: 3, unit_price_amount: 30_000, note: "" }] },
      { id: "o2", check_id: "svc1", status: "accepted", items: [{ id: "i2", product_id: "q", product_name: "Ayran", quantity: 2, unit_price_amount: 2_000, note: "" }] },
      { id: "o3", check_id: "svc1", status: "cancelled", items: [{ id: "i3", product_id: "q", product_name: "Ayran", quantity: 9, unit_price_amount: 2_000, note: "" }] },
    ],
    "/api/v1/pos/checks/svc2/orders": [],
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

function renderPicker(onPickCheck = vi.fn()) {
  render(
    wrap(
      <TablePicker branchId={BRANCH} onPick={vi.fn()} onPickCheck={onPickCheck} />,
    ),
  )
  return onPickCheck
}

beforeEach(() => {
  get.mockReset().mockImplementation((url: string) => routeGet(url))
  post.mockReset()
})

describe("TablePicker service tabs", { timeout: 20_000 }, () => {
  it("shows Masalar | Gel Al | Paket; tables stay the default", async () => {
    renderPicker()
    const tabs = screen.getAllByRole("tab")
    expect(tabs.map((tab) => tab.textContent)).toEqual([
      expect.stringContaining("Masalar"),
      expect.stringContaining("Gel Al"),
      expect.stringContaining("Paket"),
    ])
    expect(await screen.findByTestId("table-tile")).toBeInTheDocument()
    expect(screen.queryByTestId("service-check-row")).not.toBeInTheDocument()
  })

  it("Gel Al lists customer rows (name, billed item count, total) and picking one opens its check", async () => {
    const onPickCheck = renderPicker()
    fireEvent.click(screen.getByRole("tab", { name: /Gel Al/ }))

    const row = await screen.findByTestId("service-check-row")
    expect(row).toHaveTextContent("Alper Vural")
    expect(row).toHaveTextContent("940,00")
    // 3 + 2 billed; the cancelled 9 must not count.
    await waitFor(() => expect(row).toHaveTextContent("5 ürün"))
    expect(screen.queryByTestId("table-tile")).not.toBeInTheDocument()

    fireEvent.click(row)
    expect(onPickCheck).toHaveBeenCalledWith(TAKEAWAY)
  })

  it("a nameless Gel Al cannot be opened; a named one posts the right body and navigates", async () => {
    post.mockResolvedValue({ data: check({ id: "new1", service_type: "takeaway", customer_name: "Alper Vural" }) })
    const onPickCheck = renderPicker()
    fireEvent.click(screen.getByRole("tab", { name: /Gel Al/ }))
    fireEvent.click(await screen.findByRole("button", { name: /Yeni Gel Al/ }))

    const form = await screen.findByRole("dialog")
    fireEvent.click(within(form).getByRole("button", { name: "Adisyonu aç" }))
    expect(await within(form).findByText("Ad soyad girin.")).toBeInTheDocument()
    expect(post).not.toHaveBeenCalled()

    fireEvent.change(within(form).getByLabelText("Ad Soyad"), { target: { value: "  Alper Vural " } })
    fireEvent.click(within(form).getByRole("button", { name: "Adisyonu aç" }))

    await waitFor(() => expect(onPickCheck).toHaveBeenCalled())
    expect(post).toHaveBeenCalledWith("/api/v1/pos/checks", {
      branch_id: BRANCH,
      service_type: "takeaway",
      customer_name: "Alper Vural",
    })
    // No table and no empty optional fields on a takeaway body.
    const body = post.mock.calls[0][1]
    expect(body).not.toHaveProperty("table_id")
    expect(body).not.toHaveProperty("customer_phone")
    expect(onPickCheck.mock.calls[0][0]).toMatchObject({ id: "new1" })
  })

  it("Paket requires a phone; address is optional but sent when filled", async () => {
    post.mockResolvedValue({ data: check({ id: "new2", service_type: "delivery", customer_name: "Deniz Kaya" }) })
    renderPicker()
    fireEvent.click(screen.getByRole("tab", { name: /Paket/ }))
    fireEvent.click(await screen.findByRole("button", { name: /Yeni Paket/ }))

    const form = await screen.findByRole("dialog")
    const phone = within(form).getByLabelText("Telefon")
    expect(phone).toHaveAttribute("inputmode", "tel")
    fireEvent.change(within(form).getByLabelText("Ad Soyad"), { target: { value: "Deniz Kaya" } })
    fireEvent.click(within(form).getByRole("button", { name: "Adisyonu aç" }))
    expect(await within(form).findByText("Telefon numarası girin.")).toBeInTheDocument()
    expect(post).not.toHaveBeenCalled()

    fireEvent.change(phone, { target: { value: "05321234567" } })
    fireEvent.change(within(form).getByLabelText(/Adres/), { target: { value: "Serdivan" } })
    fireEvent.click(within(form).getByRole("button", { name: "Adisyonu aç" }))

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/v1/pos/checks", {
        branch_id: BRANCH,
        service_type: "delivery",
        customer_name: "Deniz Kaya",
        customer_phone: "05321234567",
        customer_address: "Serdivan",
      }),
    )
  })
})
