// Pagination wrapper all list endpoints return
export interface Paginated<T> {
  items: T[]
  total: number
  limit: number
  offset: number
}

// Catalog
export interface Product {
  id: string
  tenant_id: string
  branch_id: string | null
  category_id: string | null
  name: string
  description: string
  image_key: string
  price_amount: number
  currency: string
  sku: string
  unit: string
  tax_rate_bps: number
  is_active: boolean
  sort_order: number
  created_at: string
  updated_at: string
}
export interface Category {
  id: string
  tenant_id: string
  name: string
  description: string
  sort_order: number
  is_active: boolean
  created_at: string
  updated_at: string
}
export interface ModifierGroup {
  id: string
  tenant_id: string
  name: string
  min_selections: number
  max_selections: number
  is_required: boolean
  created_at: string
  updated_at: string
}
export interface Menu {
  id: string
  tenant_id: string
  name: string
  description: string
  is_active: boolean
  created_at: string
  updated_at: string
}

// POS
export type CheckStatus = "open" | "closed" | "cancelled"
export type OrderStatus =
  | "pending"
  | "accepted"
  | "preparing"
  | "ready"
  | "delivered"
  | "rejected"
  | "cancelled"
export interface Check {
  id: string
  tenant_id: string
  branch_id: string
  table_label: string
  status: CheckStatus
  note: string
  opened_at: string
  closed_at: string | null
}
export interface OrderItem {
  id: string
  product_id: string
  product_name: string
  quantity: number
  unit_price_amount: number
  note: string
}
export interface Order {
  id: string
  check_id: string | null
  tenant_id: string
  branch_id: string
  order_channel: string
  status: OrderStatus
  note: string
  items: OrderItem[]
  created_at: string
  updated_at: string
}

// Payment
export type PaymentMethod = "cash" | "terminal"
export interface Payment {
  id: string
  check_id: string | null
  tenant_id: string
  branch_id: string
  method: PaymentMethod
  status: string
  amount_total: number
  currency: string
  created_at: string
}

// Inventory (ADR-DATA-005 Faz 1 — warehouse-scoped, not branch-scoped)
export type StockItemKind = "raw" | "intermediate" | "packaging" | "finished"
export interface StockItem {
  id: string
  sku: string
  name: string
  kind: StockItemKind
  canonical_unit: string
  category?: string
  is_active: boolean
  created_at: string
  updated_at: string
  // Only populated by GET /stock-items (list) — resolved ADR-DATA-007 supply
  // mode for the acting principal. Absent on single-item Create/Get/Update.
  supply_mode?: SupplyMode
}

// restrictedStockItemResponse projection (ADR-DATA-007 / DATA-005 İlke 4
// revizyonu): the BTO-catalog-only shape the backend serializes for an
// exclusive_hq item viewed by a branch-scoped principal. Cost/category/
// status fields are not merely empty — the JSON keys are absent, so this is
// a distinct, narrower type rather than StockItem with optional fields.
export interface RestrictedStockItem {
  id: string
  sku: string
  name: string
  canonical_unit: string
}

// GET /stock-items renders each row as EITHER the full or restricted shape
// per item, depending on its resolved supply mode for the acting principal.
export type StockItemListEntry = StockItem | RestrictedStockItem

// Tedarik politikası (ADR-DATA-007). Immutable: no update/delete — a new
// row with a later effective_from supersedes the previous one.
export type SupplyScope = "stock_item" | "category" | "tenant_default"
export type SupplyMode = "exclusive_hq" | "approved_suppliers" | "free"

export interface SupplyPolicy {
  id: string
  branch_id?: string
  scope: SupplyScope
  stock_item_id?: string
  category?: string
  mode: SupplyMode
  approved_supplier_ids?: string[]
  effective_from: string
  created_at: string
}

// Elden fiş / faturasız alım (ADR-DATA-007 karar 3). Immutable documents —
// no update/delete route; a correction is a new receipt.
export interface PurchaseReceiptItem {
  id: string
  stock_item_id: string
  quantity: number
  unit: string
  unit_price: number
  line_total: number
  brand?: string
}

export interface PurchaseReceipt {
  id: string
  warehouse_id: string
  supplier_party_id?: string
  supplier_name?: string
  receipt_no?: string
  receipt_date: string
  total: number
  currency: string
  note?: string
  // Only populated by POST (create) and GET /{id} — the list endpoint omits
  // line items.
  items?: PurchaseReceiptItem[]
  created_at: string
}

export type WarehouseType = "depo" | "imalat"
export interface Warehouse {
  id: string
  branch_id: string
  name: string
  warehouse_type: WarehouseType
  is_active: boolean
  created_at: string
  updated_at: string
}

export interface InventoryLevel {
  id: string
  warehouse_id: string
  stock_item_id: string
  on_hand: number
  reserved: number
  available: number
  reorder_point?: number | null
  unit: string
  updated_at: string
}

export type MovementType = "in" | "out" | "adjust" | "transfer" | "reserve" | "release"
export interface InventoryTransaction {
  id: string
  warehouse_id: string
  stock_item_id: string
  movement_type: MovementType
  quantity: number
  reference_id: string | null
  reference_type: string | null
  notes: string | null
  created_at: string
}

// Billing
export type InvoiceStatus = "pending" | "sent" | "failed" | "cancelled"
export interface Invoice {
  id: string
  tenant_id: string
  payment_id: string
  amount: number
  status: InvoiceStatus
  provider: string
  created_at: string
  updated_at: string
}

// Party
export type PartyType = "customer" | "supplier" | "both"
export interface Contact {
  id: string
  type: string
  value: string
}
export interface Party {
  id: string
  tenant_id: string
  type: PartyType
  name: string
  tax_number: string
  contacts: Contact[]
  created_at: string
  updated_at: string
}

// HR
export type EmploymentType =
  | "full_time"
  | "part_time"
  | "seasonal"
  | "contractor"
export type EmployeeStatus = "active" | "on_leave" | "terminated"
export interface Employee {
  id: string
  person_id: string
  tenant_id: string
  department: string
  job_title: string
  employment_type: EmploymentType
  hire_date: string
  termination_date: string | null
  status: EmployeeStatus
  notes: string
  created_at: string
  updated_at: string
}

// Identity
export interface Me {
  id: string
  keycloak_sub: string
  email: string
  full_name: string
  created_at: string
}
export interface TenantContext {
  tenant_id: string
  tenant_name: string
  role: string
  branch_ids: string[]
}

// Tenant
export interface Branch {
  id: string
  tenant_id: string
  name: string
  slug: string
  ownership_type: string
  operation_type: string
  is_active: boolean
  phone: string
  legal_name: string
  tax_no: string
}
export interface Tenant {
  id: string
  name: string
  slug: string
  plan: string
  is_active: boolean
  created_at: string
}
