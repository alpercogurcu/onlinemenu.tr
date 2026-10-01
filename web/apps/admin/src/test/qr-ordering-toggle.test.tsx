// Branch-level "QR ile sipariş" toggle on the Masalar page, driven by the
// real use-storefront hooks against a mocked lib/api: the switch mirrors
// GET /storefront/settings, flipping it PUTs and re-reads, and a role
// without storefront.qr.manage gets neither the switch nor the GET.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import messages from "@/messages/tr.json"
import type { StorefrontSettings } from "@/types"

let granted = new Set<string>()
vi.mock("@/hooks/use-can", () => ({ useCan: (action: string) => granted.has(action) }))
vi.mock("@/hooks/use-tenant", () => ({ useBranches: () => ({ data: [{ id: "b1", name: "Serdivan" }] }) }))
vi.mock("@/lib/permissions", () => ({ currentBranchId: () => "b1" }))
vi.mock("@/store/auth-store", () => ({
  useAuthStore: (sel: (s: { tenantId: string }) => unknown) => sel({ tenantId: "t1" }),
}))
vi.mock("@/hooks/use-pos", () => ({
  useTables: () => ({ data: [], isLoading: false }),
  useZones: () => ({ data: [] }),
  useSetTableStatus: () => ({ mutateAsync: vi.fn(), isPending: false }),
}))

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))

vi.mock("@/lib/api", () => ({
  default: {
    get: (...args: unknown[]) => get(...args),
    put: (...args: unknown[]) => put(...args),
  },
}))

import TablesPage from "@/app/(main)/pos/tables/page"

function Wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <NextIntlClientProvider locale="tr" messages={messages}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </NextIntlClientProvider>
  )
}

const MANAGER = ["pos.table.read", "storefront.qr.read", "storefront.qr.manage"]
const CASHIER = ["pos.table.read", "storefront.qr.read"]

// In-memory stand-in for the settings row: PUT writes it, the invalidation
// refetch reads it back — the same round trip the real server does.
let serverSettings: StorefrontSettings

function wireApi() {
  get.mockImplementation(async () => ({ data: serverSettings }))
  put.mockImplementation(async (_url: string, body: StorefrontSettings) => {
    serverSettings = { ...body }
    return { data: serverSettings }
  })
}

function renderPage() {
  return render(<TablesPage />, { wrapper: Wrapper })
}

describe("TablesPage QR ordering toggle", () => {
  beforeEach(() => {
    granted = new Set(MANAGER)
    serverSettings = { branch_id: "b1", ordering_enabled: true }
    get.mockReset()
    put.mockReset()
    wireApi()
  })

  it("reflects the GET state on the switch", async () => {
    renderPage()

    const toggle = await screen.findByRole("switch", { name: "QR ile sipariş" })
    await waitFor(() => expect(toggle).toBeChecked())
    expect(get).toHaveBeenCalledWith("/api/v1/storefront/settings", { params: { branch_id: "b1" } })
    expect(screen.queryByText(/misafir menüyü görür, sipariş veremez/)).not.toBeInTheDocument()
  })

  it("shows the off hint and PUTs the new state when flipped on", async () => {
    serverSettings = { branch_id: "b1", ordering_enabled: false }
    renderPage()

    const toggle = await screen.findByRole("switch", { name: "QR ile sipariş" })
    await waitFor(() => expect(toggle).not.toBeDisabled())
    expect(toggle).not.toBeChecked()
    expect(screen.getByText("Kapalı — misafir menüyü görür, sipariş veremez")).toBeInTheDocument()

    fireEvent.click(toggle)

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/v1/storefront/settings", {
        branch_id: "b1",
        ordering_enabled: true,
      }),
    )
    await waitFor(() => expect(toggle).toBeChecked())
    expect(screen.queryByText(/misafir menüyü görür, sipariş veremez/)).not.toBeInTheDocument()
  })

  it("PUTs ordering_enabled=false when flipped off", async () => {
    renderPage()

    const toggle = await screen.findByRole("switch", { name: "QR ile sipariş" })
    await waitFor(() => expect(toggle).toBeChecked())
    await waitFor(() => expect(toggle).not.toBeDisabled())

    fireEvent.click(toggle)

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/v1/storefront/settings", {
        branch_id: "b1",
        ordering_enabled: false,
      }),
    )
    await waitFor(() => expect(toggle).not.toBeChecked())
    await screen.findByText("Kapalı — misafir menüyü görür, sipariş veremez")
  })

  it("renders no switch and fires no GET without storefront.qr.manage", async () => {
    granted = new Set(CASHIER)
    renderPage()

    expect(screen.queryByRole("switch")).not.toBeInTheDocument()
    // The settings query is permission-gated (enabled: canViewQR), so a
    // read-only role must not even issue the request.
    await waitFor(() => expect(get).not.toHaveBeenCalled())
  })
})
