"use client"

import { useRouter, useSearchParams } from "next/navigation"
import { Suspense, useEffect, useRef } from "react"

import { OrderScreen } from "@/components/pos/order/order-screen"
import { TablePicker } from "@/components/pos/order/table-picker"
import { Skeleton } from "@/components/ui/skeleton"
import { useSelectedBranch } from "@/hooks/use-selected-branch"

// /pos/order              -> table plan / gel al / paket picker (step 1)
// /pos/order?branch=&table= -> order screen for that table
// /pos/order?branch=&check= -> order screen for a tableless (gel al / paket) check
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
  // The branch lives in the global header switcher (use-selected-branch);
  // ?branch= only remains as the deep-link entry point below.
  const { branchId: selectedBranchId, branches, locked, setBranch } = useSelectedBranch()

  const paramBranch = params.get("branch") ?? ""
  const paramBranchValid = paramBranch !== "" && branches.some((b) => b.id === paramBranch)
  const tableId = params.get("table") ?? ""
  const checkId = params.get("check") ?? ""

  // Deep-link contract: an incoming ?branch= is written into the store ONCE
  // (so every other page follows the link), after which the store leads and
  // the URL is kept in step. Without the consumed ref the two effects below
  // would fight each other whenever the header switcher changes the store
  // while a stale param is still in the URL.
  const consumedParamRef = useRef<string | null>(null)
  useEffect(() => {
    if (locked || selectedBranchId === "") return
    if (paramBranchValid && consumedParamRef.current !== paramBranch) {
      // First sight of this param: it is the deep link, mark it consumed
      // even when it already matches so a later header switch is never
      // mistaken for a fresh link back to the old branch.
      consumedParamRef.current = paramBranch
      if (paramBranch !== selectedBranchId) setBranch(paramBranch)
      return
    }
    if (paramBranch !== "" && paramBranch !== selectedBranchId) {
      // The store moved on (header switch): refresh the URL and drop any
      // table/check — they belong to the previous branch.
      router.replace(`/pos/order?${new URLSearchParams({ branch: selectedBranchId }).toString()}`)
    }
  }, [locked, paramBranch, paramBranchValid, selectedBranchId, setBranch, router])

  // Render from the param while it is being consumed so the deep-linked
  // branch's tables never flash the previously selected branch first.
  const branchId = locked ? selectedBranchId : paramBranchValid ? paramBranch : selectedBranchId

  function go(nextBranch: string, next?: { table?: string; check?: string }) {
    const query = new URLSearchParams({ branch: nextBranch })
    if (next?.table) query.set("table", next.table)
    if (next?.check) query.set("check", next.check)
    router.push(`/pos/order?${query.toString()}`)
  }

  if (tableId && branchId) {
    return <OrderScreen key={tableId} branchId={branchId} tableId={tableId} onBackToTables={() => go(branchId)} />
  }

  if (checkId && branchId) {
    return <OrderScreen key={checkId} branchId={branchId} serviceCheckId={checkId} onBackToTables={() => go(branchId)} />
  }

  return (
    <TablePicker
      branchId={branchId}
      onPick={(table) => go(branchId, { table: table.id })}
      onPickCheck={(check) => go(branchId, { check: check.id })}
    />
  )
}
