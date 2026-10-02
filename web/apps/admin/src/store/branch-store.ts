import { create } from "zustand"
import { createJSONStorage, persist, type StateStorage } from "zustand/middleware"

// Global branch selection (the header switcher). Selections are keyed by
// "<tenantId>:<userId>" so a different account (or the same person on a
// different tenant) in the same browser never inherits someone else's
// branch — the localStorage blob is shared, the entries are not.
//
// Branch-scoped operators (ctx.branch_id set — waiter/cashier/kitchen) never
// read or write this store: use-selected-branch.ts pins them to their own
// branch regardless of what is persisted here.
interface BranchState {
  selections: Record<string, string>
  setBranch: (scopeKey: string, branchId: string) => void
}

export function branchScopeKey(tenantId: string | null | undefined, userId: string | null | undefined): string {
  return `${tenantId ?? ""}:${userId ?? ""}`
}

// localStorage is not reliably available (SSR, private mode, jsdom under
// vitest — see kds-theme.test.tsx / use-device-dark.ts for the same
// precedent). Every call is guarded so a broken storage degrades to an
// in-memory-only selection instead of crashing the store.
const safeLocalStorage: StateStorage = {
  getItem: (name) => {
    try {
      return window.localStorage.getItem(name)
    } catch {
      return null
    }
  },
  setItem: (name, value) => {
    try {
      window.localStorage.setItem(name, value)
    } catch {
      // Selection still lives in memory for this page load.
    }
  },
  removeItem: (name) => {
    try {
      window.localStorage.removeItem(name)
    } catch {
      // Nothing to remove if nothing could be written.
    }
  },
}

export const useBranchStore = create<BranchState>()(
  persist(
    (set) => ({
      selections: {},
      setBranch: (scopeKey, branchId) =>
        set((state) => ({ selections: { ...state.selections, [scopeKey]: branchId } })),
    }),
    {
      name: "admin-branch-selection",
      storage: createJSONStorage(() => safeLocalStorage),
      partialize: (state) => ({ selections: state.selections }),
    },
  ),
)
