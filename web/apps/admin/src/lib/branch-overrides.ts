import type { BranchSaleState } from "@/lib/status-badge"
import type { BranchProductOverride } from "@/types"

// A stored row with an empty price and the product available reads exactly
// like "no row" (ADR-DATA-009 §1: clearing the price leaves such a row
// behind), so "does this product differ on the branch" must look at the
// values, never at whether a row exists. Sale-only: a branch cost does not
// change how the product is sold, so it is not counted here.
export function overrideDiffers(row: BranchProductOverride | undefined): boolean {
  if (!row) return false
  return row.price_amount !== null || !row.is_available
}

// Any stored deviation, including a branch cost. Drives the pricing screen's
// count, filter and reset button so a cost-only row stays listed and resettable.
export function overrideHasDeviation(row: BranchProductOverride | undefined): boolean {
  return overrideDiffers(row) || (row?.cost_amount ?? null) !== null
}

export function branchSaleState(row: BranchProductOverride | undefined): BranchSaleState {
  if (!row) return "tenant"
  if (!row.is_available) return "closed"
  return row.price_amount !== null ? "branch" : "tenant"
}
