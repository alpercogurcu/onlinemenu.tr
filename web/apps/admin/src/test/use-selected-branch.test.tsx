// The global branch selection (header switcher) — store resolution rules:
// persisted choice wins while it is still a real branch, a stale id falls
// back to the first branch, selections are scoped per tenant+user so another
// account on the same browser never inherits them, and a branch-scoped role
// (ctx.branch_id) locks the selection to its own branch no matter what was
// stored. Storage is stubbed the way kds-theme.test.tsx does it — jsdom's
// localStorage is not reliably available under vitest.
import { act, renderHook } from "@testing-library/react"
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest"

let branches: { id: string; name: string }[] = []
vi.mock("@/hooks/use-tenant", () => ({
  useBranches: () => ({ data: branches, isLoading: false }),
}))

let scopedBranchId: string | null = null
vi.mock("@/lib/permissions", () => ({ currentBranchId: () => scopedBranchId }))

import { useSelectedBranch } from "@/hooks/use-selected-branch"
import { useAuthStore } from "@/store/auth-store"
import { branchScopeKey, useBranchStore } from "@/store/branch-store"

const BRANCHES = [
  { id: "b1", name: "Adapazarı" },
  { id: "b2", name: "Serdivan" },
]

function signIn(userId: string, tenantId = "t1") {
  useAuthStore.setState({ user: { id: userId, name: "Test", email: "t@x" }, tenantId })
}

beforeAll(() => {
  const backing = new Map<string, string>()
  Object.defineProperty(window, "localStorage", {
    configurable: true,
    value: {
      getItem: (k: string) => backing.get(k) ?? null,
      setItem: (k: string, v: string) => void backing.set(k, v),
      removeItem: (k: string) => void backing.delete(k),
      clear: () => backing.clear(),
    },
  })
})

beforeEach(() => {
  branches = BRANCHES
  scopedBranchId = null
  window.localStorage.clear()
  useBranchStore.setState({ selections: {} })
  signIn("u1")
})

describe("useSelectedBranch", () => {
  it("falls back to the first branch when nothing is stored", () => {
    const { result } = renderHook(() => useSelectedBranch())
    expect(result.current.branchId).toBe("b1")
    expect(result.current.branch?.name).toBe("Adapazarı")
    expect(result.current.locked).toBe(false)
  })

  it("setBranch changes the selection and persists it", () => {
    const { result } = renderHook(() => useSelectedBranch())
    act(() => result.current.setBranch("b2"))
    expect(result.current.branchId).toBe("b2")

    // The write reached storage (what a page reload reads back).
    const raw = window.localStorage.getItem("admin-branch-selection")
    expect(raw).not.toBeNull()
    expect(JSON.parse(raw!).state.selections[branchScopeKey("t1", "u1")]).toBe("b2")
  })

  it("rehydrates a stored selection (page reload)", async () => {
    window.localStorage.setItem(
      "admin-branch-selection",
      JSON.stringify({ state: { selections: { [branchScopeKey("t1", "u1")]: "b2" } }, version: 0 }),
    )
    await act(() => useBranchStore.persist.rehydrate())

    const { result } = renderHook(() => useSelectedBranch())
    expect(result.current.branchId).toBe("b2")
  })

  it("a persisted id that is no longer in the list falls back to the first branch", () => {
    useBranchStore.setState({ selections: { [branchScopeKey("t1", "u1")]: "gone" } })
    const { result } = renderHook(() => useSelectedBranch())
    expect(result.current.branchId).toBe("b1")
  })

  it("selections are scoped per tenant+user — another account never inherits them", () => {
    const { result, rerender } = renderHook(() => useSelectedBranch())
    act(() => result.current.setBranch("b2"))
    expect(result.current.branchId).toBe("b2")

    signIn("u2")
    rerender()
    expect(result.current.branchId).toBe("b1")

    signIn("u1")
    rerender()
    expect(result.current.branchId).toBe("b2")
  })

  it("a branch-scoped role is locked to ctx.branch_id and setBranch is a no-op", () => {
    scopedBranchId = "b2"
    useBranchStore.setState({ selections: { [branchScopeKey("t1", "u1")]: "b1" } })
    const { result } = renderHook(() => useSelectedBranch())

    expect(result.current.locked).toBe(true)
    expect(result.current.branchId).toBe("b2")

    act(() => result.current.setBranch("b1"))
    expect(result.current.branchId).toBe("b2")
    expect(useBranchStore.getState().selections[branchScopeKey("t1", "u1")]).toBe("b1")
  })
})
