import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import api from "@/lib/api"
import type {
  InventoryLevel,
  InventoryTransaction,
  StockItem,
  StockItemKind,
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

// Stock items
export function useStockItems(params?: { kind?: StockItemKind }) {
  return useQuery({
    queryKey: ["stock-items", params],
    queryFn: async () => {
      const { data } = await api.get<StockItem[]>("/api/v1/inventory/stock-items", { params })
      return data ?? []
    },
  })
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
