"use client"

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import api, { PUBLIC_API_PREFIX } from "@/lib/api"
import type {
  MenuResponse,
  Order,
  OrderListResponse,
  PlaceOrderRequest,
  PlaceOrderResponse,
} from "@/types/storefront"
import { isTerminalStatus } from "@/types/storefront"

export const MENU_QUERY_KEY = ["storefront", "menu"] as const
export const ORDERS_QUERY_KEY = ["storefront", "orders"] as const

// Mirrors the API's `Cache-Control: private, max-age=30`. The menu is fetched
// from the BROWSER, never rendered on the server: the guest cookie is scoped
// `Path=/api/public/v1`, so it is never attached to a page navigation and the
// Next server literally cannot see it. Caching this response in an ISR/route
// cache would also be a cross-tenant leak — one cache entry, many restaurants.
const MENU_STALE_TIME_MS = 30_000

const ORDER_POLL_INTERVAL_MS = 5_000
const ORDER_LIST_POLL_INTERVAL_MS = 15_000

export function useMenu() {
  return useQuery({
    queryKey: MENU_QUERY_KEY,
    queryFn: async () => {
      const { data } = await api.get<MenuResponse>(`${PUBLIC_API_PREFIX}/menu`)
      return data.categories ?? []
    },
    staleTime: MENU_STALE_TIME_MS,
    // 401 means "re-scan the QR"; retrying cannot fix it and the interceptor
    // has already started the redirect.
    retry: 1,
  })
}

export function useMyOrders() {
  return useQuery({
    queryKey: ORDERS_QUERY_KEY,
    queryFn: async () => {
      const { data } = await api.get<OrderListResponse>(`${PUBLIC_API_PREFIX}/orders`)
      return data.orders ?? []
    },
    refetchInterval: ORDER_LIST_POLL_INTERVAL_MS,
    retry: 1,
  })
}

export function useOrder(orderId: string) {
  return useQuery({
    queryKey: [...ORDERS_QUERY_KEY, orderId],
    queryFn: async () => {
      const { data } = await api.get<Order>(`${PUBLIC_API_PREFIX}/orders/${orderId}`)
      return data
    },
    enabled: orderId !== "",
    // Status tracking is polling in the MVP (SSE/WS deferred). Polling stops
    // once the order reaches a state it can no longer leave, so a phone left
    // on the table does not keep the surface's rate limit budget burning.
    refetchInterval: (query) =>
      query.state.data && isTerminalStatus(query.state.data.status)
        ? false
        : ORDER_POLL_INTERVAL_MS,
    retry: 1,
  })
}

export interface PlaceOrderVariables {
  body: PlaceOrderRequest
  /**
   * Created ONCE per submission by the cart store and reused on every retry —
   * a fresh key per attempt would turn one timed-out request into two real
   * orders (ADR-SEC-003).
   */
  idempotencyKey: string
}

export function usePlaceOrder() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ body, idempotencyKey }: PlaceOrderVariables) => {
      const { data } = await api.post<PlaceOrderResponse>(
        `${PUBLIC_API_PREFIX}/orders`,
        body,
        { headers: { "Idempotency-Key": idempotencyKey } },
      )
      return data
    },
    // Retries are disabled here on purpose: an automatic retry would race the
    // caller's own error handling for a mutation whose safety rests entirely
    // on the key above. The diner retries explicitly, with the same key.
    retry: false,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ORDERS_QUERY_KEY })
    },
  })
}
