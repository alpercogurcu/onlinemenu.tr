import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import api from "@/lib/api"
import type { Check, Order, OrderStatus } from "@/types"

export function useChecks(params?: {
  status?: string
  limit?: number
  refetchInterval?: number
}) {
  const { refetchInterval, ...queryParams } = params ?? {}
  return useQuery({
    queryKey: ["checks", queryParams],
    queryFn: async () => {
      const { data } = await api.get<Check[]>("/api/v1/pos/checks", { params: queryParams })
      return data ?? []
    },
    refetchInterval,
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

export function useOrder(orderId: string) {
  return useQuery({
    queryKey: ["orders", orderId],
    queryFn: async () => {
      const { data } = await api.get<Order>(`/api/v1/pos/orders/${orderId}`)
      return data
    },
    enabled: Boolean(orderId),
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
      void qc.invalidateQueries({ queryKey: ["orders"] })
    },
  })
}

// useAdvanceOrder requires an explicit target status: the backend handler
// (backend/internal/modules/pos/http/handler.go's advanceOrder) decodes a
// {"status": "..."} JSON body and validates it against the order status
// machine (domain.TransitionOrderStatus) — it does not compute "next status"
// itself. A bodiless POST 400s with "invalid request body" (json.Decode on
// an empty body returns io.EOF).
export function useAdvanceOrder() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, status }: { id: string; status: OrderStatus }) =>
      api.post(`/api/v1/pos/orders/${id}/advance`, { status }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["checks"] })
      void qc.invalidateQueries({ queryKey: ["orders"] })
    },
  })
}
