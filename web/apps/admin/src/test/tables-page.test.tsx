// Masalar as the counter and the kitchen see it (2026-09-23 prod sweep):
//   - a cashier holds storefront.qr.read but not .manage; the QR dialog then
//     offered only greyed-out "İptal et / Yenile" (the raw token is never
//     re-shown), so the button is simply not there for them;
//   - an occupied table links straight to its adisyon;
//   - the subtitle no longer tells a cashier to "define zones and tables".
import { render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { beforeEach, describe, expect, it, vi } from "vitest"

import messages from "@/messages/tr.json"

let granted = new Set<string>()
vi.mock("@/hooks/use-can", () => ({ useCan: (action: string) => granted.has(action) }))
vi.mock("@/hooks/use-tenant", () => ({ useBranches: () => ({ data: [{ id: "b1", name: "Serdivan" }] }) }))
vi.mock("@/lib/permissions", () => ({ currentBranchId: () => "b1" }))
vi.mock("@/store/auth-store", () => ({
  useAuthStore: (sel: (s: { tenantId: string }) => unknown) => sel({ tenantId: "t1" }),
}))
vi.mock("@/hooks/use-pos", () => ({
  useTables: () => ({
    data: [
      {
        zone_id: "z1",
        zone_name: "Salon",
        tables: [
          { id: "t1", name: "Masa 1", capacity: 4, status: "occupied", active_check_id: "c1", zone_id: "z1" },
          { id: "t2", name: "Masa 2", capacity: 4, status: "empty", active_check_id: null, zone_id: "z1" },
        ],
      },
    ],
    isLoading: false,
  }),
  useZones: () => ({ data: [{ id: "z1", name: "Salon", floor: 0, is_active: true, sort_order: 0 }] }),
  useSetTableStatus: () => ({ mutateAsync: vi.fn(), isPending: false }),
}))

import TablesPage from "@/app/(main)/pos/tables/page"

function renderPage() {
  return render(
    <NextIntlClientProvider locale="tr" messages={messages}>
      <TablesPage />
    </NextIntlClientProvider>,
  )
}

const CASHIER = ["pos.table.read", "pos.table.clean", "pos.order.place", "pos.check.read", "storefront.qr.read"]

describe("TablesPage", () => {
  beforeEach(() => {
    granted = new Set(CASHIER)
  })

  it("shows no QR button to a role that cannot issue codes", () => {
    renderPage()
    expect(screen.queryByRole("button", { name: /QR/ })).not.toBeInTheDocument()
  })

  it("shows the QR button to a role that can issue codes", () => {
    granted = new Set([...CASHIER, "storefront.qr.manage"])
    renderPage()
    expect(screen.getAllByRole("button", { name: /QR/ }).length).toBeGreaterThan(0)
  })

  it("links an occupied table to its adisyon for a role that reads checks", () => {
    renderPage()
    expect(screen.getByRole("link", { name: "Masa 1 adisyonunu aç" })).toHaveAttribute("href", "/pos/checks/c1")
  })

  it("offers no adisyon link without pos.check.read (kitchen)", () => {
    granted = new Set(["pos.table.read"])
    renderPage()
    expect(screen.queryByRole("link", { name: "Masa 1 adisyonunu aç" })).not.toBeInTheDocument()
  })

  it("describes the page by what the role can do", () => {
    renderPage()
    expect(screen.queryByText(/Bölge ve masa tanımlayın/)).not.toBeInTheDocument()
    granted = new Set([...CASHIER, "pos.table.manage"])
    renderPage()
    expect(screen.getByText(/Bölge ve masa tanımlayın/)).toBeInTheDocument()
  })
})
