import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import api from "@/lib/api"
import type { Check, Order } from "@/types"

export function useChecks(params?: { status?: string; limit?: number }) {
  return useQuery({
    queryKey: ["checks", params],
    queryFn: async () => {
      const { data } = await api.get<Check[]>("/api/v1/pos/checks", { params })
      return data ?? []
    },
  })
}

export function useCheck(id: string) {
  return useQuery({
    queryKey: ["checks", id],
    queryFn: async () => {
      const { data } = await api.get<Check>(`/api/v1/pos/checks/${id}`)
      return data
    },
    enabled: Boolean(id),
  })
}

export function useCheckOrders(checkId: string) {
  return useQuery({
    queryKey: ["checks", checkId, "orders"],
    queryFn: async () => {
      const { data } = await api.get<Order[]>(`/api/v1/pos/checks/${checkId}/orders`)
      return data ?? []
    },
    enabled: Boolean(checkId),
  })
}

export function useCreateCheck() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Partial<Check>) => api.post<Check>("/api/v1/pos/checks", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["checks"] })
    },
  })
}

export function useCloseCheck() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.post(`/api/v1/pos/checks/${id}/close`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["checks"] })
    },
  })
}

export function useCancelCheck() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.post(`/api/v1/pos/checks/${id}/cancel`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["checks"] })
    },
  })
}

export function useCreateOrder() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Partial<Order>) => api.post<Order>("/api/v1/pos/orders", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["checks"] })
    },
  })
}

export function useAcceptOrder() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.post(`/api/v1/pos/orders/${id}/accept`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["checks"] })
    },
  })
}

export function useAdvanceOrder() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.post(`/api/v1/pos/orders/${id}/advance`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["checks"] })
    },
  })
}
