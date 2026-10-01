// "POS Tercihleri" card on the Şubeler page, driven by the real
// use-pos-branch-settings hooks against a mocked lib/api: the radios mirror
// GET /pos/branch-settings, changing one PUTs only the edited field, and a
// role without pos.table.manage gets neither the card nor the GET.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import messages from "@/messages/tr.json"
import type { PosBranchSettings } from "@/types"

let granted = new Set<string>()
vi.mock("@/hooks/use-can", () => ({ useCan: (action: string) => granted.has(action) }))
vi.mock("@/hooks/use-tenant", () => ({
  branchesQueryKey: (tenantId: string) => ["branches", tenantId],
  useTenant: () => ({ data: undefined }),
  useBranches: () => ({
    data: [
      { id: "b1", name: "Serdivan" },
      { id: "b2", name: "Adapazarı" },
    ],
    isLoading: false,
  }),
}))
vi.mock("@/store/auth-store", () => ({
  useAuthStore: (sel: (s: { tenantId: string }) => unknown) => sel({ tenantId: "t1" }),
}))

const { get, put, post } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))

vi.mock("@/lib/api", () => ({
  default: {
    get: (...args: unknown[]) => get(...args),
    put: (...args: unknown[]) => put(...args),
    post: (...args: unknown[]) => post(...args),
  },
}))

import BranchesPage from "@/app/(main)/settings/branches/page"

function Wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <NextIntlClientProvider locale="tr" messages={messages}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </NextIntlClientProvider>
  )
}

const MANAGER = ["pos.table.manage"]

// In-memory stand-in for the settings rows: PUT merges into them, the
// invalidation refetch reads them back — the same round trip the real
// server does, defaults included (no row → top/full).
let serverSettings: Record<string, PosBranchSettings>

function wireApi() {
  get.mockImplementation(async (_url: string, opts: { params: { branch_id: string } }) => {
    const branchId = opts.params.branch_id
    return {
      data: serverSettings[branchId] ?? {
        branch_id: branchId,
        waiter_category_layout: "top",
        order_flow: "full",
      },
    }
  })
  put.mockImplementation(async (_url: string, body: Partial<PosBranchSettings> & { branch_id: string }) => {
    const current = serverSettings[body.branch_id] ?? {
      branch_id: body.branch_id,
      waiter_category_layout: "top",
      order_flow: "full",
    }
    serverSettings[body.branch_id] = { ...current, ...body }
    return { data: serverSettings[body.branch_id] }
  })
}

function renderPage() {
  return render(<BranchesPage />, { wrapper: Wrapper })
}

const LAYOUT_TOP = "Üstte yatay (kaydırmalı)"
const LAYOUT_SIDE = "Solda dikey liste"
const FLOW_FULL = /Tam — mutfak ekranıyla/
const FLOW_SIMPLE = /Basit — mutfak fişiyle/

describe("BranchesPage POS preferences", () => {
  beforeEach(() => {
    granted = new Set(MANAGER)
    serverSettings = {}
    get.mockReset()
    put.mockReset()
    post.mockReset()
    wireApi()
  })

  it("reflects the GET state of the first branch on the radios", async () => {
    serverSettings.b1 = { branch_id: "b1", waiter_category_layout: "side", order_flow: "simple" }
    renderPage()

    const side = await screen.findByRole("radio", { name: LAYOUT_SIDE })
    await waitFor(() => expect(side).toBeChecked())
    expect(screen.getByRole("radio", { name: FLOW_SIMPLE })).toBeChecked()
    expect(screen.getByRole("radio", { name: LAYOUT_TOP })).not.toBeChecked()
    expect(screen.getByRole("radio", { name: FLOW_FULL })).not.toBeChecked()
    expect(get).toHaveBeenCalledWith("/api/v1/pos/branch-settings", { params: { branch_id: "b1" } })
    expect(screen.getByText("Basit akışta mutfak ekranı ve sipariş durum takibi devre dışı kalır.")).toBeInTheDocument()
  })

  it("defaults to top/full when the branch has no stored row", async () => {
    renderPage()

    const top = await screen.findByRole("radio", { name: LAYOUT_TOP })
    await waitFor(() => expect(top).toBeChecked())
    expect(screen.getByRole("radio", { name: FLOW_FULL })).toBeChecked()
  })

  it("PUTs only the layout field when the layout changes", async () => {
    renderPage()

    const side = await screen.findByRole("radio", { name: LAYOUT_SIDE })
    await waitFor(() => expect(side).not.toBeDisabled())

    fireEvent.click(side)

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/v1/pos/branch-settings", {
        branch_id: "b1",
        waiter_category_layout: "side",
      }),
    )
    await waitFor(() => expect(side).toBeChecked())
    expect(screen.getByRole("radio", { name: FLOW_FULL })).toBeChecked()
  })

  it("PUTs only the flow field when the flow changes", async () => {
    renderPage()

    const simple = await screen.findByRole("radio", { name: FLOW_SIMPLE })
    await waitFor(() => expect(simple).not.toBeDisabled())

    fireEvent.click(simple)

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/v1/pos/branch-settings", {
        branch_id: "b1",
        order_flow: "simple",
      }),
    )
    await waitFor(() => expect(simple).toBeChecked())
  })

  it("fetches the selected branch's settings when the branch changes", async () => {
    serverSettings.b2 = { branch_id: "b2", waiter_category_layout: "side", order_flow: "full" }
    renderPage()

    await screen.findByRole("radio", { name: LAYOUT_TOP })
    fireEvent.change(screen.getByLabelText("Şube"), { target: { value: "b2" } })

    await waitFor(() =>
      expect(get).toHaveBeenCalledWith("/api/v1/pos/branch-settings", { params: { branch_id: "b2" } }),
    )
    await waitFor(() => expect(screen.getByRole("radio", { name: LAYOUT_SIDE })).toBeChecked())
  })

  it("renders no card and fires no GET without pos.table.manage", async () => {
    granted = new Set<string>()
    renderPage()

    expect(screen.queryByText("POS Tercihleri")).not.toBeInTheDocument()
    expect(screen.queryByRole("radio")).not.toBeInTheDocument()
    // The settings query is permission-gated (enabled: canManage), so a
    // role without the permission must not even issue the request.
    await waitFor(() => expect(get).not.toHaveBeenCalled())
  })
})
