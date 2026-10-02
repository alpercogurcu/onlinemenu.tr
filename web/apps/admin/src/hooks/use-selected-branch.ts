"use client"

import { useBranches } from "@/hooks/use-tenant"
import { currentBranchId } from "@/lib/permissions"
import { branchScopeKey, useBranchStore } from "@/store/branch-store"
import { useAuthStore } from "@/store/auth-store"
import type { Branch } from "@/types"

export interface SelectedBranch {
  /** The branch every branch-keyed screen should show. "" until known. */
  branchId: string
  branch: Branch | undefined
  branches: Branch[]
  /**
   * True for a branch-scoped operator (ctx.branch_id in the CTX token —
   * waiter/cashier/kitchen): the switcher shows their branch and setBranch
   * is a no-op. Chain-wide principals (manager) switch freely.
   */
  locked: boolean
  isLoading: boolean
  setBranch: (branchId: string) => void
}

/**
 * Single source of the selected branch for the whole admin app. The branch
 * is chosen ONCE in the header (BranchSwitcher) and every branch-dependent
 * page reads it from here — per-page branch selects are gone.
 *
 * Resolution order:
 *   1. ctx.branch_id (branch-scoped role) — always wins, selector locked;
 *   2. the persisted choice for this tenant+user, if it still exists;
 *   3. the first branch of the list.
 */
export function useSelectedBranch(): SelectedBranch {
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""
  // Subscribing to `user` also forces a re-render when the session (and so
  // the CTX token currentBranchId reads) changes — same trick as useCan.
  const user = useAuthStore((s) => s.user)
  const scopeKey = branchScopeKey(tenantId, user?.id)

  const { data, isLoading } = useBranches(tenantId)
  const branches = data ?? []

  const stored = useBranchStore((s) => s.selections[scopeKey])
  const setStored = useBranchStore((s) => s.setBranch)

  const scopedBranchId = user ? currentBranchId() : null
  const locked = scopedBranchId !== null

  const inList = (id: string | null | undefined): id is string =>
    typeof id === "string" && id !== "" && branches.some((b) => b.id === id)

  // A stale persisted id (branch deleted, access revoked) falls back to the
  // first branch instead of rendering an empty/403'd screen forever.
  const branchId = locked ? scopedBranchId : inList(stored) ? stored : (branches[0]?.id ?? "")

  const setBranch = (id: string) => {
    if (locked) return
    setStored(scopeKey, id)
  }

  return {
    branchId,
    branch: branches.find((b) => b.id === branchId),
    branches,
    locked,
    isLoading,
    setBranch,
  }
}
