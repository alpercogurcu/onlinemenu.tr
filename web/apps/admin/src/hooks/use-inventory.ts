import { useQuery } from "@tanstack/react-query"

import api from "@/lib/api"
import type { InventoryLevel, InventoryTransaction } from "@/types"

export function useInventoryLevels(params?: { branch_id?: string }) {
  return useQuery({
    queryKey: ["inventory-levels", params],
    queryFn: async () => {
      const { data } = await api.get<InventoryLevel[]>("/api/v1/inventory/levels", { params })
      return data ?? []
    },
    enabled: Boolean(params?.branch_id),
  })
}

export function useInventoryTransactions(params?: { branch_id?: string }) {
  return useQuery({
    queryKey: ["inventory-transactions", params],
    queryFn: async () => {
      const { data } = await api.get<InventoryTransaction[]>("/api/v1/inventory/transactions", { params })
      return data ?? []
    },
    enabled: Boolean(params?.branch_id),
  })
}
