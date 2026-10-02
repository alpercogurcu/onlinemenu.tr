"use client"

import { Store } from "lucide-react"

import { Select, SelectItem } from "@/components/ui/select"
import { useSelectedBranch } from "@/hooks/use-selected-branch"

// Global branch switcher in the top bar: the branch is picked HERE, once,
// and every branch-dependent page follows via use-selected-branch.ts.
// Branch-scoped roles (waiter/cashier/kitchen) see their own branch as a
// read-only badge — offering them a Select would only lead to empty/403'd
// screens for branches they cannot touch.
export default function BranchSwitcher() {
  const { branchId, branch, branches, locked, setBranch } = useSelectedBranch()

  if (locked) {
    return (
      <span
        data-testid="branch-switcher-locked"
        className="border-input bg-muted text-foreground inline-flex h-8 max-w-40 items-center gap-1.5 rounded-md border px-2.5 text-sm font-medium sm:max-w-56"
        title="Şubeniz — rolünüz bu şubeye bağlı"
      >
        <Store className="text-muted-foreground size-3.5 shrink-0" aria-hidden="true" />
        <span className="truncate">{branch?.name ?? "—"}</span>
      </span>
    )
  }

  if (branches.length === 0) return null

  return (
    <div className="flex items-center gap-1.5">
      <Store className="text-muted-foreground hidden size-4 shrink-0 sm:block" aria-hidden="true" />
      <Select
        aria-label="Şube seçimi"
        className="h-8 w-36 font-medium sm:w-48"
        value={branchId}
        onValueChange={setBranch}
      >
        {branches.map((b) => (
          <SelectItem key={b.id} value={b.id}>
            {b.name}
          </SelectItem>
        ))}
      </Select>
    </div>
  )
}
