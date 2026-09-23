// Adisyon detail as the cashier meets it on the web panel (2026-09-23 prod
// sweep). The web takes no payments, so an unpaid check's "Kapat" is locked
// with the reason next to it; "İptal" asks first; a waiter's order waiting
// for the counter can be accepted right here instead of on the kitchen board.
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { beforeEach, describe, expect, it, vi } from "vitest"

import messages from "@/messages/tr.json"
import type { Check, CheckSettlement, Order } from "@/types"

const cancelMutate = vi.fn()
const acceptMutate = vi.fn()
let canAccept = true
let settlement: CheckSettlement = { check_id: "c1", as_of: "", completed: [], pending_total: 0 }
let orders: Order[] = []

const check: Check = {
  id: "c1",
  tenant_id: "t",
  branch_id: "b",
  table_label: "Masa 1",
  pax: 2,
  status: "open",
  note: "",
  opened_at: "2026-09-23T10:00:00Z",
  closed_at: null,
  total: 94_000,
}

vi.mock("@/hooks/use-can", () => ({
  useCan: (action: string) => (action === "pos.order.accept" ? canAccept : true),
}))
vi.mock("@/hooks/use-pos", () => ({
  useCheck: () => ({ data: check, isLoading: false, isError: false }),
  useCheckOrders: () => ({ data: orders, isLoading: false, isError: false }),
  useCheckSettlement: () => ({ data: settlement, isLoading: false, isError: false }),
  useCloseCheck: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useCancelCheck: () => ({ mutateAsync: cancelMutate, isPending: false }),
  useAcceptOrder: () => ({ mutateAsync: acceptMutate, isPending: false }),
}))
vi.mock("next/navigation", () => ({ usePathname: () => "/pos/checks/c1" }))
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

import { CheckDetail } from "@/components/pos/check-detail"

function order(status: Order["status"]): Order {
  return {
    id: "o1",
    check_id: "c1",
    tenant_id: "t",
    branch_id: "b",
    order_channel: "dine_in",
    status,
    note: "",
    items: [{ id: "i1", product_id: "p1", product_name: "Smash", quantity: 2, unit_price_amount: 47_000, note: "" }],
    created_at: "2026-09-23T10:01:00Z",
    updated_at: "2026-09-23T10:01:00Z",
  }
}

function renderDetail() {
  return render(
    <NextIntlClientProvider locale="tr" messages={messages}>
      <CheckDetail checkId="c1" />
    </NextIntlClientProvider>,
  )
}

describe("CheckDetail", () => {
  beforeEach(() => {
    cancelMutate.mockReset()
    acceptMutate.mockReset()
    canAccept = true
    settlement = { check_id: "c1", as_of: "", completed: [], pending_total: 0 }
    orders = [order("accepted")]
  })

  it("locks 'Kapat' on an unpaid check and says where the payment is taken", () => {
    renderDetail()
    expect(screen.getByRole("button", { name: "Kapat" })).toBeDisabled()
    expect(screen.getByText(/POS uygulamasından/)).toBeInTheDocument()
  })

  it("leaves 'Kapat' usable once the check is paid", () => {
    settlement = { check_id: "c1", as_of: "", completed: [{ payment_id: "p", amount_total: 94_000 }], pending_total: 0 }
    renderDetail()
    expect(screen.getByRole("button", { name: "Kapat" })).toBeEnabled()
    expect(screen.queryByText(/POS uygulamasından/)).not.toBeInTheDocument()
  })

  it("asks before cancelling", async () => {
    cancelMutate.mockResolvedValue({})
    renderDetail()
    fireEvent.click(screen.getByRole("button", { name: "İptal" }))
    expect(cancelMutate).not.toHaveBeenCalled()
    const dialog = await screen.findByRole("alertdialog")
    fireEvent.click(within(dialog).getByRole("button", { name: "Adisyonu iptal et" }))
    await waitFor(() => expect(cancelMutate).toHaveBeenCalledWith("c1"))
  })

  it("accepts a pending order from the check for a counter role", async () => {
    orders = [order("pending")]
    acceptMutate.mockResolvedValue({})
    renderDetail()
    fireEvent.click(screen.getByRole("button", { name: "Siparişi kabul et" }))
    await waitFor(() => expect(acceptMutate).toHaveBeenCalledWith("o1"))
  })

  it("offers no accept button without pos.order.accept", () => {
    orders = [order("pending")]
    canAccept = false
    renderDetail()
    expect(screen.queryByRole("button", { name: "Siparişi kabul et" })).not.toBeInTheDocument()
  })
})
