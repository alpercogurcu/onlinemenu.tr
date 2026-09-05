import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect, useRef, useState } from "react"

import api from "@/lib/api"
import type { Check, Order, OrderStatus, PosTable, PosTableStatus, PosZone, PosZonePlan } from "@/types"

// useTables returns the branch floor plan already grouped by zone — that is
// the backend's response shape (zonePlanResponse), not a client-side grouping,
// so a single request renders zone-labelled sections with no follow-up call to
// GET /zones. branch_id is required by the endpoint (422 without it), hence the
// enabled guard rather than an optional param.
export function useTables(branchId: string, params?: { refetchInterval?: number }) {
  return useQuery({
    queryKey: ["pos-tables", branchId],
    queryFn: async () => {
      const { data } = await api.get<PosZonePlan[]>("/api/v1/pos/tables", {
        params: { branch_id: branchId },
      })
      return data ?? []
    },
    enabled: branchId !== "",
    refetchInterval: params?.refetchInterval,
  })
}

// useZones lists the branch's zones, including the ones that have no table
// yet — GET /tables cannot substitute for it (see the PosZone doc comment).
export function useZones(branchId: string) {
  return useQuery({
    queryKey: ["pos-zones", branchId],
    queryFn: async () => {
      const { data } = await api.get<PosZone[]>("/api/v1/pos/zones", {
        params: { branch_id: branchId },
      })
      return data ?? []
    },
    enabled: branchId !== "",
  })
}

// Zone and table writes both change the floor plan the tables page renders,
// so every mutation below invalidates BOTH caches: a renamed zone shows up in
// the plan (zone_name is denormalised into it) and a new table shows up in the
// zone it was attached to.
function invalidatePlan(qc: ReturnType<typeof useQueryClient>) {
  void qc.invalidateQueries({ queryKey: ["pos-zones"] })
  void qc.invalidateQueries({ queryKey: ["pos-tables"] })
}

export function useCreateZone() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: { branch_id: string; name: string; floor: number }) => {
      const { data } = await api.post<PosZone>("/api/v1/pos/zones", body)
      return data
    },
    onSuccess: () => invalidatePlan(qc),
  })
}

// PATCH semantics: only the keys present in `patch` are sent, so an omitted
// field keeps its stored value (backend service.ZonePatch relies on this —
// sending `is_active: undefined` is fine, JSON.stringify drops it, but sending
// an explicit null would not be).
export function useUpdateZone() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({
      id,
      ...patch
    }: {
      id: string
      name?: string
      floor?: number
      is_active?: boolean
    }) => {
      const { data } = await api.patch<PosZone>(`/api/v1/pos/zones/${id}`, patch)
      return data
    },
    onSuccess: () => invalidatePlan(qc),
  })
}

export function useCreateTable() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: {
      branch_id: string
      zone_id: string
      name: string
      capacity: number
    }) => {
      const { data } = await api.post<PosTable>("/api/v1/pos/tables", body)
      return data
    },
    onSuccess: () => invalidatePlan(qc),
  })
}

export function useUpdateTable() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({
      id,
      ...patch
    }: {
      id: string
      zone_id?: string
      name?: string
      capacity?: number
      is_active?: boolean
    }) => {
      const { data } = await api.patch<PosTable>(`/api/v1/pos/tables/${id}`, patch)
      return data
    },
    onSuccess: () => invalidatePlan(qc),
  })
}

// "occupied" is deliberately not offerable by callers: the backend's
// TableService.SetStatus rejects it outright (ErrManualOccupyForbidden) —
// a table only becomes occupied as a side effect of opening a check.
export type ManualTableStatus = Exclude<PosTableStatus, "occupied">

export function useSetTableStatus() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, status }: { id: string; status: ManualTableStatus }) => {
      const { data } = await api.post<PosTable>(`/api/v1/pos/tables/${id}/status`, { status })
      return data
    },
    onSuccess: () => invalidatePlan(qc),
  })
}

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

const ORDER_DETAILS_BATCH = 100

// useOrderDetails resolves order details for a live board in batches, so a
// kitchen display with N tickets makes ceil(N/100) requests instead of N.
// It keeps its own ledger (ref) of ids already asked for — including ids the
// backend never returned, which are not asked again — so a newly arrived
// ticket costs exactly one request carrying only that id.
export function useOrderDetails(ids: string[]): Map<string, Order> {
  const [details, setDetails] = useState<Map<string, Order>>(() => new Map())
  const requested = useRef<Set<string>>(new Set())
  // The KDS board (allOrderIds) is a flatMap over kitchen columns, so a ticket
  // moving from one column to another reorders `ids` without changing the
  // set. Keying the effect on a sorted copy means that reorder does not
  // re-run the effect (and does not re-request ids already resolved).
  const idsKey = [...ids].sort().join(",")
  // Read by fetch callbacks that may settle after `ids` moved on, so a result
  // for a ticket that has since left the board is never resurrected into the
  // map. Updated every render (not just on effect runs) so it is always the
  // truly latest board, independent of whether idsKey changed.
  const latestIds = useRef<Set<string>>(new Set())
  latestIds.current = new Set(ids)
  // Only false after unmount — NOT on every effect re-run — so a fetch still
  // in flight when the id list changes keeps writing its result into state
  // instead of being discarded.
  const alive = useRef(true)

  useEffect(() => {
    return () => {
      alive.current = false
    }
  }, [])

  useEffect(() => {
    const pending = ids.filter((id) => !requested.current.has(id))
    for (const id of pending) requested.current.add(id)

    // Drop tickets that left the board so the map never grows unbounded.
    setDetails((prev) => {
      const keep = new Set(ids)
      let changed = false
      const next = new Map<string, Order>()
      for (const [id, order] of prev) {
        if (keep.has(id)) next.set(id, order)
        else changed = true
      }
      return changed ? next : prev
    })

    if (pending.length === 0) return
    const batches: string[][] = []
    for (let i = 0; i < pending.length; i += ORDER_DETAILS_BATCH) {
      batches.push(pending.slice(i, i + ORDER_DETAILS_BATCH))
    }
    for (const batch of batches) {
      api
        .get<Order[]>("/api/v1/pos/orders", { params: { ids: batch.join(",") } })
        .then(({ data }) => {
          if (!alive.current) return
          setDetails((prev) => {
            const next = new Map(prev)
            for (const order of data ?? []) {
              if (latestIds.current.has(order.id)) next.set(order.id, order)
            }
            return next
          })
        })
        .catch(() => {
          // Let the next id-list change retry this batch instead of looping
          // forever — the KDS WebSocket reconnect already re-snapshots the
          // board, so no logging is needed here.
          for (const id of batch) requested.current.delete(id)
        })
    }
    // idsKey is the order-independent identity of `ids`; the array itself is
    // rebuilt (and reordered on column moves) each render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [idsKey])

  return details
}
