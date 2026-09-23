"use client"

import { useRouter, useSearchParams } from "next/navigation"
import { Suspense } from "react"

import { OrderScreen } from "@/components/pos/order/order-screen"
import { TablePicker } from "@/components/pos/order/table-picker"
import { Skeleton } from "@/components/ui/skeleton"
import { useBranches } from "@/hooks/use-tenant"
import { currentBranchId } from "@/lib/permissions"
import { useAuthStore } from "@/store/auth-store"

// /pos/order              -> table plan (step 1)
// /pos/order?branch=&table= -> order screen for that table
//
// Kept in the URL (not component state) so the browser back button walks the
// same path the waiter did, and the tables page can link straight to a table.
// The CTX token is memory-only, so every move goes through the SPA router.
export default function OrderPage() {
  return (
    // useSearchParams needs a Suspense boundary or `next build` refuses the page.
    <Suspense fallback={<Skeleton className="h-64 w-full rounded-2xl" />}>
      <OrderRoute />
    </Suspense>
  )
}

function OrderRoute() {
  const params = useSearchParams()
  const router = useRouter()
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""
  const { data: branches } = useBranches(tenantId)

  // A branch-scoped operator always works their own branch, whatever the URL
  // says; a chain-wide one (manager) picks, defaulting to the first branch.
  const scopedBranchId = currentBranchId()
  const branchId = scopedBranchId ?? (params.get("branch") || branches?.[0]?.id || "")
  const tableId = params.get("table") ?? ""

  function go(nextBranch: string, nextTable?: string) {
    const query = new URLSearchParams({ branch: nextBranch })
    if (nextTable) query.set("table", nextTable)
    router.push(`/pos/order?${query.toString()}`)
  }

  if (tableId && branchId) {
    return <OrderScreen key={tableId} branchId={branchId} tableId={tableId} onBackToTables={() => go(branchId)} />
  }

  return (
    <TablePicker
      branchId={branchId}
      branches={branches}
      branchLocked={scopedBranchId !== null}
      onBranchChange={(next) => go(next)}
      onPick={(table) => go(branchId, table.id)}
    />
  )
}
