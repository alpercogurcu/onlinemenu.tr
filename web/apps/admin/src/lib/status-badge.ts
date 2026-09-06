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
      return "neutral"
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
    case "pending":
    case "invited":
      return "warning"
    default:
      return "neutral"
  }
}
