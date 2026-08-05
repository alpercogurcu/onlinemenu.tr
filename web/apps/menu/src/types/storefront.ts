// Wire types of /api/public/v1 — mirrors docs/plans/2026-08-05-wp2-endpoint-contract.md
// and backend/internal/modules/storefront/http/dto.go one field at a time.
// Money is always kuruş (int64 on the wire); nothing here is a float.

export interface SessionResponse {
  branch_id: string
  table_id: string
  table_label: string
  expires_at: string
}

export interface MenuModifier {
  id: string
  name: string
  price_delta: number
}

// selection_type "single" means the SERVER accepts at most one selection from
// this group even when max_select is 0 — 0 only means "unbounded" for
// "multiple" groups. Rendering checkboxes for a single group produces a 422.
export type ModifierSelectionType = "single" | "multiple"

export interface MenuModifierGroup {
  id: string
  name: string
  selection_type: ModifierSelectionType
  min_select: number
  max_select: number
  modifiers: MenuModifier[]
}

export interface MenuProduct {
  id: string
  name: string
  description: string
  price_amount: number
  currency: string
  // Object-storage key, NOT a URL. No signer exists anywhere in the platform
  // yet; lib/media.ts composes a URL from NEXT_PUBLIC_MEDIA_BASE_URL or gives
  // up and shows a placeholder.
  image_key: string
  // Always [] today — the catalog schema has no allergen column. The UI hides
  // the section rather than building an empty affordance.
  allergens: string[]
  is_available: boolean
  modifier_groups: MenuModifierGroup[]
}

export interface MenuCategory {
  // The all-zero UUID is the synthetic "uncategorised" bucket; an empty name
  // renders as the localized "Diğer".
  id: string
  name: string
  sort_order: number
  products: MenuProduct[]
}

export interface MenuResponse {
  categories: MenuCategory[]
}

export interface PlaceOrderLineRequest {
  product_id: string
  quantity: number
  modifier_ids: string[]
  note: string
}

// No price field: the server re-derives every amount from the menu read model
// (ADR-ARCH-006 §6). Sending one would not be rejected — it is undecodable.
export interface PlaceOrderRequest {
  lines: PlaceOrderLineRequest[]
  note: string
}

export interface PlaceOrderResponse {
  order_id: string
  check_id: string
  status: string
  total: number
}

export const ORDER_STATUSES = [
  "pending",
  "accepted",
  "preparing",
  "ready",
  "delivered",
  "rejected",
  "cancelled",
] as const

export type OrderStatus = (typeof ORDER_STATUSES)[number]

export interface OrderItem {
  name: string
  quantity: number
  unit_price_amount: number
  note: string
}

export interface Order {
  order_id: string
  status: OrderStatus
  note: string
  items: OrderItem[]
  total: number
  created_at: string
  updated_at: string
}

export interface OrderListResponse {
  orders: Order[]
}

// Statuses after which polling is pointless — the order will not change again.
const TERMINAL_STATUSES: ReadonlySet<string> = new Set<OrderStatus>([
  "delivered",
  "rejected",
  "cancelled",
])

export function isTerminalStatus(status: string): boolean {
  return TERMINAL_STATUSES.has(status)
}
