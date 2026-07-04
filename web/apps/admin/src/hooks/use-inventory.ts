import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import api from "@/lib/api"
import type {
  InventoryLevel,
  InventoryTransaction,
  PurchaseReceipt,
  RestrictedStockItem,
  StockItem,
  StockItemKind,
  StockItemListEntry,
  SupplyMode,
  SupplyPolicy,
  SupplyScope,
  Warehouse,
  WarehouseType,
} from "@/types"

// Stock levels — warehouse-scoped (ADR-DATA-005). warehouse_id is a required
// query param on the backend (400 Bad Request if missing).
export function useStockLevels(params: { warehouse_id?: string }) {
  return useQuery({
    queryKey: ["inventory-levels", params],
    queryFn: async () => {
      const { data } = await api.get<InventoryLevel[]>("/api/v1/inventory/levels", { params })
      return data ?? []
    },
    enabled: Boolean(params.warehouse_id),
  })
}

// Stock movements — warehouse_id required, stock_item_id/limit optional.
export function useStockMovements(params: {
  warehouse_id?: string
  stock_item_id?: string
  limit?: number
}) {
  return useQuery({
    queryKey: ["inventory-movements", params],
    queryFn: async () => {
      const { data } = await api.get<InventoryTransaction[]>("/api/v1/inventory/movements", {
        params,
      })
      return data ?? []
    },
    enabled: Boolean(params.warehouse_id),
  })
}

// Warehouses
export function useWarehouses(params?: { branch_id?: string }) {
  return useQuery({
    queryKey: ["warehouses", params],
    queryFn: async () => {
      const { data } = await api.get<Warehouse[]>("/api/v1/inventory/warehouses", { params })
      return data ?? []
    },
  })
}

export interface CreateWarehousePayload {
  branch_id: string
  name: string
  warehouse_type: WarehouseType
  is_active?: boolean
}

export function useCreateWarehouse() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateWarehousePayload) =>
      api.post<Warehouse>("/api/v1/inventory/warehouses", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["warehouses"] })
    },
  })
}

// Stock items — GET renders each row as EITHER the full projection OR the
// restricted BTO-catalog-only projection per item (ADR-DATA-007), depending
// on the resolved supply mode for the acting principal.
export function useStockItems(params?: { kind?: StockItemKind }) {
  return useQuery({
    queryKey: ["stock-items", params],
    queryFn: async () => {
      const { data } = await api.get<StockItemListEntry[]>("/api/v1/inventory/stock-items", {
        params,
      })
      return data ?? []
    },
  })
}

// isRestrictedStockItem discriminates the two GET /stock-items row shapes:
// a restricted row structurally lacks `kind` (present in the full shape),
// since the backend omits the JSON key entirely rather than sending it
// blank (docs/lessons-from-b2b.md: visibility is field absence).
export function isRestrictedStockItem(
  item: StockItemListEntry,
): item is RestrictedStockItem {
  return !("kind" in item)
}

export interface CreateStockItemPayload {
  sku: string
  name: string
  kind: StockItemKind
  canonical_unit: string
  category?: string
  is_active?: boolean
}

export function useCreateStockItem() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateStockItemPayload) =>
      api.post<StockItem>("/api/v1/inventory/stock-items", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["stock-items"] })
    },
  })
}

// Supply policies (ADR-DATA-007) — immutable: create + list/read only, no
// update/delete route. A new row with a later effective_from supersedes the
// previous one; the backend never mutates an existing row.
export function useSupplyPolicies() {
  return useQuery({
    queryKey: ["supply-policies"],
    queryFn: async () => {
      const { data } = await api.get<SupplyPolicy[]>("/api/v1/inventory/supply-policies")
      return data ?? []
    },
  })
}

export interface CreateSupplyPolicyPayload {
  scope: SupplyScope
  stock_item_id?: string
  category?: string
  mode: SupplyMode
  approved_supplier_ids?: string[]
  // RFC3339 timestamp (backend parses with time.RFC3339-style layout, NOT
  // date-only) — convert a plain date input via `new Date(v).toISOString()`.
  // Omit to let the backend default to time.Now().
  effective_from?: string
}

export function useCreateSupplyPolicy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateSupplyPolicyPayload) =>
      api.post<SupplyPolicy>("/api/v1/inventory/supply-policies", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["supply-policies"] })
    },
  })
}

// Purchase receipts (ADR-DATA-007 karar 3) — elden fiş / faturasız alım.
// warehouse_id is a required query param on the backend (400 without it),
// mirroring useStockLevels/useStockMovements.
export function usePurchaseReceipts(params: { warehouse_id?: string }) {
  return useQuery({
    queryKey: ["purchase-receipts", params],
    queryFn: async () => {
      const { data } = await api.get<PurchaseReceipt[]>("/api/v1/inventory/purchase-receipts", {
        params,
      })
      return data ?? []
    },
    enabled: Boolean(params.warehouse_id),
  })
}

export interface CreatePurchaseReceiptItemPayload {
  stock_item_id: string
  quantity: number
  unit: string
  unit_price: number
  // Optional — defaults to quantity*unit_price server-side when omitted/0.
  line_total?: number
  brand?: string
}

export interface CreatePurchaseReceiptPayload {
  warehouse_id: string
  supplier_party_id?: string
  supplier_name?: string
  receipt_no?: string
  // Date-only "YYYY-MM-DD" (distinct from supply-policy's effective_from
  // format) — defaults to today server-side when omitted.
  receipt_date?: string
  total?: number
  currency?: string
  note?: string
  items: CreatePurchaseReceiptItemPayload[]
}

export function useCreatePurchaseReceipt() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreatePurchaseReceiptPayload) =>
      api.post<PurchaseReceipt>("/api/v1/inventory/purchase-receipts", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["purchase-receipts"] })
    },
  })
}
