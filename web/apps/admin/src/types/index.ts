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
  // ADR-DATA-005 product↔stock-item link. Backend serializes this with
  // `omitempty` — absent from the JSON entirely (not `null`) when the
  // product has no stock backing, hence optional here rather than always
  // `string | null`. Every product PUT must round-trip it (see
  // toProductBody in components/catalog/product-editor.tsx) or a save
  // silently drops the link.
  source_stock_item_id?: string | null
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
export type SelectionType = "single" | "multiple"
export interface ModifierGroup {
  id: string
  tenant_id: string
  name: string
  selection_type: SelectionType
  min_selections: number
  // null means unlimited (only meaningful for selection_type "multiple").
  max_selections: number | null
  is_required: boolean
  sort_order: number
  created_at: string
  updated_at: string
}
export interface Modifier {
  id: string
  tenant_id: string
  group_id: string
  name: string
  // Kuruş (int64), may be negative — see components/catalog/money-input.tsx
  // allowNegative.
  price_delta: number
  is_active: boolean
  sort_order: number
  created_at: string
  updated_at: string
}

// bps (basis points, 1/100 of a percent): %0, %1, %10, %20.
export const TAX_RATE_OPTIONS = [0, 100, 1000, 2000] as const
export const UNIT_OPTIONS = ["adet", "porsiyon", "gram", "kg", "ml", "lt"] as const
export interface Menu {
  id: string
  tenant_id: string
  name: string
  description: string
  is_active: boolean
  created_at: string
  updated_at: string
}

// A product placed on a menu (backend menuItemResponse). The row carries no
// product name — the UI resolves it from the products list — and no
// sort_order: the create endpoint accepts one, but the read DTO omits it.
export interface MenuItem {
  menu_id: string
  product_id: string
  tenant_id: string
  // Kuruş (int64) or null when the menu uses the product's own price.
  price_override: number | null
  is_active: boolean
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
// Which surface opened the check (backend domain.Source). OPTIONAL on
// purpose: the value exists on the row and is set to "online_qr" by the
// storefront flow, but the REST DTO (checkResponse in
// backend/internal/modules/pos/http/handler.go) does not serialize it yet, so
// today it is always undefined over the wire. The UI therefore renders the
// source badge only when the field is actually present.
export type CheckSource = "pos" | "online_qr"

export interface Check {
  id: string
  tenant_id: string
  branch_id: string
  table_label: string
  source?: CheckSource
  // Guest count (kişi sayısı). Always present — lives directly on
  // domain.Check, no extra query needed (see backend checkResponse doc).
  pax: number
  status: CheckStatus
  note: string
  opened_at: string
  closed_at: string | null
  // Kuruş (int64). Only populated by the list and get endpoints (batch/
  // single total query) — open/close/cancel responses omit it entirely
  // (omitempty), so it must stay optional here rather than nullable.
  total?: number
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

// Fiscal (ADR-FISCAL-001/002) — Token X Connect Cloud terminal directory and
// the category -> device VAT section mapping used to route sale lines.
export type BasketMode = "instant" | "list"
export interface FiscalTerminal {
  id: string
  tenant_id: string
  branch_id: string
  vendor: string
  terminal_serial: string
  vendor_merchant_ref: string
  vendor_branch_ref: string
  label: string
  basket_mode: BasketMode
  is_active: boolean
  created_at: string
  updated_at: string
}
// A VAT "section" the device itself reports (synced_at reflects the last
// successful sync-sections call for that specific row, not the terminal).
export interface FiscalSection {
  section_no: number
  name: string
  tax_permyriad: number
  synced_at: string
}
export interface FiscalSectionMapping {
  category_id: string
  section_no: number
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
// Mirrors membershipResponse in
// backend/internal/modules/identity/http/membership_handler.go —
// person_name/person_email/role_name are joined server-side so the user
// list never fans out one request per row.
export interface Membership {
  id: string
  person_id: string
  person_name: string
  person_email: string
  tenant_id: string
  branch_id?: string
  role_id: string
  role_name: string
  status: MembershipStatus
}

export type MembershipStatus = "active" | "suspended" | "terminated"

export interface MembershipListResponse {
  memberships: Membership[]
}

// Mirrors personDTO in backend/internal/modules/identity/http/me_handler.go.
// GET /v1/identity/me wraps it as { person: Me } — see MeResponse.
export interface Me {
  id: string
  email: string
  full_name: string
  phone: string
}

export interface MeResponse {
  person: Me
}
// Mirrors contextItemDTO in
// backend/internal/modules/identity/http/me_handler.go — one selectable
// membership (tenant + optional branch + role).
export interface TenantContext {
  membership_id: string
  tenant_id: string
  tenant_name: string
  branch_id?: string
  branch_name?: string
  role_id: string
  role_name: string
}

// GET /v1/identity/me/contexts response envelope.
export interface TenantContextListResponse {
  contexts: TenantContext[]
  customer: boolean
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
  // Module keys the tenant has bought (pos, catalog, inventory, …); absent on
  // older backends — see lib/modules.ts for how the sidebar treats that.
  enabled_modules?: string[]
  is_active: boolean
  created_at: string
}

// POS table plan — GET /api/v1/pos/tables?branch_id= returns the floor plan
// already grouped by zone (backend zonePlanResponse), not a flat table list.
export type PosTableStatus = "empty" | "occupied" | "reserved" | "cleaning"

export interface PosTable {
  id: string
  branch_id: string
  zone_id: string
  name: string
  capacity: number
  status: PosTableStatus
  layout_position: unknown
  is_active: boolean
  active_check_id: string | null
}

export interface PosZonePlan {
  zone_id: string
  zone_name: string
  floor: number
  tables: PosTable[]
}

// GET /api/v1/pos/zones?branch_id= (backend zoneResponse). Needed in addition
// to the zone plan above because the plan is built by grouping TABLE rows: a
// zone with no tables yet never appears in it, and that is exactly the zone a
// "add table" form has to offer right after the zone was created.
export interface PosZone {
  id: string
  branch_id: string
  name: string
  floor: number
  is_active: boolean
}

// Storefront — table QR codes (ADR-ARCH-006).
export type QRCodeStatus = "active" | "revoked"

// Mirrors the backend qrCodeResponse exactly. There is deliberately no token
// field: the raw token is returned ONCE, by create/rotate, inside
// IssuedQRCode — and the hash is never exposed at all.
export interface QRCode {
  id: string
  tenant_id: string
  branch_id: string
  table_id: string
  table_label: string
  status: QRCodeStatus
  created_by: string
  revoked_at: string | null
  revoked_by: string | null
  created_at: string
  updated_at: string
}

// Create/rotate response. `token` exists only in this response body and must
// never be persisted (no localStorage, no query cache, no component state that
// outlives the dialog) — the server keeps only its hash, so it cannot be
// re-read, and anything that stores it becomes a second place it can leak from.
export interface IssuedQRCode {
  qr_code: QRCode
  token: string
}

// Sales report — GET /api/v1/pos/reports/sale-details response
// (backend/internal/modules/pos/repo/report_repo.go). Every amount is kuruş
// (int64); render with lib/money.ts's formatKurus, never divide by 100 here.
export type ReportPaymentMethod =
  | "cash"
  | "terminal"
  | "meal_card"
  | "comp"
  | "no_charge"
  | "open_account"
export type ReportPaymentStatus = "completed" | "voided"

export interface SaleDetailsSummary {
  closed_check_count: number
  gross: number
  item_count: number
  average_check: number
}
export interface SaleDetailsCancellations {
  check_count: number
  amount: number
}
export interface SaleDetailsTaxRate {
  rate_bps: number
  gross: number
  base: number
  tax: number
}
export interface SaleDetailsByDay {
  date: string
  gross: number
  check_count: number
}
export interface SaleDetailsBySource {
  source: string
  check_count: number
  gross: number
}
export interface SaleDetailsPayment {
  method: ReportPaymentMethod
  status: ReportPaymentStatus
  count: number
  total: number
}
// A cash session row. `closed_at`, `closing_counted_amount` and `difference`
// are null for a session still open (or, for `difference`, one closed without
// a counted amount) — see docs/adr/DATA-008.
export interface SaleDetailsCashSession {
  id: string
  status: string
  opened_at: string
  closed_at: string | null
  opening_counted_amount: number
  cash_payments_taken: number
  movements_net: number
  expected_close: number
  closing_counted_amount: number | null
  difference: number | null
}

export interface SaleDetails {
  branch_id: string
  from: string
  to: string
  tz: string
  sales: SaleDetailsSummary
  cancellations: SaleDetailsCancellations
  by_tax_rate: SaleDetailsTaxRate[]
  by_day: SaleDetailsByDay[]
  by_source: SaleDetailsBySource[]
  payments: SaleDetailsPayment[]
  cash_sessions: SaleDetailsCashSession[]
}
