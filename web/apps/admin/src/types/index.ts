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
  base_price: number
  is_active: boolean
  image_url: string
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
export type PaymentMethod = "cash" | "card" | "online"
export interface Payment {
  id: string
  check_id: string
  tenant_id: string
  amount: number
  method: PaymentMethod
  status: string
  created_at: string
}

// Inventory
export interface InventoryLevel {
  id: string
  product_id: string
  branch_id: string
  quantity: number
  updated_at: string
}

export interface InventoryTransaction {
  id: string
  product_id: string
  branch_id: string
  type: string
  quantity_delta: number
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
