import type { BranchSaleState } from "@/lib/status-badge"
import type { BranchProductOverride } from "@/types"

// A stored row with an empty price and the product available reads exactly
// like "no row" (ADR-DATA-009 §1: clearing the price leaves such a row
// behind), so "does this product differ on the branch" must look at the
// values, never at whether a row exists.
export function overrideDiffers(row: BranchProductOverride | undefined): boolean {
  if (!row) return false
  return row.price_amount !== null || !row.is_available
}

export function branchSaleState(row: BranchProductOverride | undefined): BranchSaleState {
  if (!row) return "tenant"
  if (!row.is_available) return "closed"
  return row.price_amount !== null ? "branch" : "tenant"
}
