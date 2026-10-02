// /pos/order deep-link contract after the global switcher refactor: an
// incoming ?branch= is written into the branch store once (so the rest of
// the app follows the link), a later header switch rewrites the URL instead
// of snapping back to the stale param, and a branch-scoped role ignores the
// param entirely.
import { render, screen, waitFor } from "@testing-library/react"
import { act } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

const { push, replace, getParams, setParams } = vi.hoisted(() => {
  let params = new URLSearchParams()
  return {
    push: vi.fn(),
    replace: vi.fn(),
    getParams: () => params,
    setParams: (qs: string) => {
      params = new URLSearchParams(qs)
    },
  }
})

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push, replace }),
  useSearchParams: () => getParams(),
}))

let branches: { id: string; name: string }[] = []
vi.mock("@/hooks/use-tenant", () => ({
  useBranches: () => ({ data: branches, isLoading: false }),
}))

let scopedBranchId: string | null = null
vi.mock("@/lib/permissions", () => ({ currentBranchId: () => scopedBranchId }))

vi.mock("@/components/pos/order/table-picker", () => ({
  TablePicker: ({ branchId }: { branchId: string }) => <div data-testid="picker">{branchId}</div>,
}))
vi.mock("@/components/pos/order/order-screen", () => ({
  OrderScreen: ({ branchId, tableId }: { branchId: string; tableId?: string }) => (
    <div data-testid="order-screen">{`${branchId}:${tableId ?? ""}`}</div>
  ),
}))

import OrderPage from "@/app/(main)/pos/order/page"
import { useAuthStore } from "@/store/auth-store"
import { branchScopeKey, useBranchStore } from "@/store/branch-store"

const SCOPE = () => branchScopeKey("t1", "u1")

beforeEach(() => {
  branches = [
    { id: "b1", name: "Adapazarı" },
    { id: "b2", name: "Serdivan" },
  ]
  scopedBranchId = null
  setParams("")
  push.mockReset()
  replace.mockReset()
  useBranchStore.setState({ selections: {} })
  useAuthStore.setState({ user: { id: "u1", name: "Test", email: "t@x" }, tenantId: "t1" })
})

describe("/pos/order ?branch= sync", () => {
  it("writes a deep-linked ?branch= into the global store", async () => {
    setParams("branch=b2")
    render(<OrderPage />)

    expect(screen.getByTestId("picker")).toHaveTextContent("b2")
    await waitFor(() =>
      expect(useBranchStore.getState().selections[SCOPE()]).toBe("b2"),
    )
  })

  it("without a param it renders the globally selected branch and leaves the URL alone", async () => {
    useBranchStore.setState({ selections: { [SCOPE()]: "b2" } })
    render(<OrderPage />)

    expect(screen.getByTestId("picker")).toHaveTextContent("b2")
    await waitFor(() => expect(replace).not.toHaveBeenCalled())
  })

  it("a header switch rewrites a stale param instead of snapping back to it", async () => {
    setParams("branch=b1&table=t9")
    render(<OrderPage />)
    expect(screen.getByTestId("order-screen")).toHaveTextContent("b1:t9")

    act(() => {
      useBranchStore.getState().setBranch(SCOPE(), "b2")
    })

    // The table belongs to the old branch, so it is dropped with the param.
    await waitFor(() => expect(replace).toHaveBeenCalledWith("/pos/order?branch=b2"))
    expect(useBranchStore.getState().selections[SCOPE()]).toBe("b2")
  })

  it("a branch-scoped role ignores the param and stays on its own branch", async () => {
    scopedBranchId = "b1"
    setParams("branch=b2")
    render(<OrderPage />)

    expect(screen.getByTestId("picker")).toHaveTextContent("b1")
    await waitFor(() => expect(useBranchStore.getState().selections[SCOPE()]).toBeUndefined())
  })
})
