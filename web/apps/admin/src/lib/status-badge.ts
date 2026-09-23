import type { CheckStatus, OrderStatus, PosTableStatus } from "@/types"

// One place that decides which semantic colour a domain status carries, so
// pages never encode "green means active" themselves and both themes stay in
// step (the Badge variants read the status tokens in globals.css).
export type StatusBadgeVariant = "success" | "warning" | "info" | "danger" | "neutral"

export function productStatusVariant(isActive: boolean): StatusBadgeVariant {
  return isActive ? "success" : "neutral"
}

export function checkStatusVariant(status: CheckStatus): StatusBadgeVariant {
  switch (status) {
    case "open":
      return "warning"
    case "closed":
      return "success"
    case "cancelled":
      return "neutral"
    // A merged adisyon is not a failure and not a sale: its money is billed
    // on the check that absorbed it. "info" keeps it visually apart from both
    // the green "closed" and the grey "cancelled".
    case "merged":
      return "info"
  }
}

export function orderStatusVariant(status: OrderStatus): StatusBadgeVariant {
  switch (status) {
    case "pending":
      return "warning"
    case "accepted":
    case "preparing":
      return "info"
    case "ready":
    case "delivered":
      return "success"
    case "rejected":
      return "danger"
    case "cancelled":
      return "neutral"
  }
}

export function tableStatusVariant(status: PosTableStatus): StatusBadgeVariant {
  switch (status) {
    case "empty":
      return "neutral"
    case "occupied":
      return "warning"
    case "reserved":
      return "info"
    case "cleaning":
      return "warning"
  }
}

export function paymentStatusVariant(status: string): StatusBadgeVariant {
  switch (status) {
    case "completed":
      return "success"
    case "pending":
      return "info"
    case "failed":
    case "voided":
      return "danger"
    default:
      return "neutral"
  }
}

export function membershipStatusVariant(status: string): StatusBadgeVariant {
  switch (status) {
    case "active":
      return "success"
    default:
      return "neutral"
  }
}

// How a product sells on one branch (ADR-DATA-009): at the tenant price,
// at the branch's own price, or not at all. "closed" outranks a price — a
// product that is off the menu has no meaningful "selling price" to flag.
export type BranchSaleState = "tenant" | "branch" | "closed"

export function branchSaleStateVariant(state: BranchSaleState): StatusBadgeVariant {
  switch (state) {
    case "tenant":
      return "neutral"
    case "branch":
      return "info"
    case "closed":
      return "warning"
  }
}
