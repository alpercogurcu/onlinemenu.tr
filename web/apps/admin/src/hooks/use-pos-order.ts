import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useCallback } from "react"

import api from "@/lib/api"
import {
  optionGroupsFrom,
  sellableProducts,
  type OptionLookup,
  type PlaceOrderBody,
  type ProductOptionsWire,
} from "@/lib/pos-order"
import type { Category, Check, Order, Product } from "@/types"

// The web order screen (/pos/order) reads the catalog in the shape a waiter
// needs, not the shape the catalog editor needs, so it has its own hooks:
//
// * Products are always asked for WITH the table's branch_id. A manager is
//   chain-wide, and without the parameter the backend falls back to tenant
//   prices — the screen would then read one price aloud and the order would
//   come back 422 price_mismatch at a branch with an override (ADR-DATA-009).
// * Everything is cached for a couple of minutes: tapping through categories
//   must never wait on the network. The server re-prices every line at order
//   time, so a stale cache costs a rejected order, never a wrong charge.
const CATALOG_STALE_MS = 2 * 60_000

export function useOrderCategories() {
  return useQuery({
    queryKey: ["pos-order", "categories"],
    queryFn: async () => {
      const { data } = await api.get<Category[]>("/api/v1/catalog/categories")
      return (data ?? [])
        .filter((c) => c.is_active)
        .sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name, "tr"))
    },
    staleTime: CATALOG_STALE_MS,
  })
}

export function useBranchProducts(branchId: string) {
  return useQuery({
    queryKey: ["pos-order", "products", branchId],
    queryFn: async () => {
      const { data } = await api.get<Product[]>("/api/v1/catalog/products", {
        params: { branch_id: branchId },
      })
      return sellableProducts(data ?? [])
    },
    enabled: branchId !== "",
    staleTime: CATALOG_STALE_MS,
  })
}

const optionTreeKey = (branchId: string) => ["pos-order", "option-tree", branchId] as const

async function fetchOptionTree(branchId: string): Promise<Map<string, ProductOptionsWire["groups"]>> {
  const { data } = await api.get<ProductOptionsWire[]>("/api/v1/catalog/products/modifier-groups", {
    params: { branch_id: branchId },
  })
  return new Map((data ?? []).map((p) => [p.product_id, p.groups]))
}

/**
 * Option groups of every sellable product, from ONE request
 * (GET /catalog/products/modifier-groups). It replaced a request per visible
 * product plus one per group, which the production per-IP rate limit (shared
 * by every phone behind the restaurant's router) would eventually throttle.
 *
 * `optionsFor` is synchronous: undefined means "tree not loaded yet" (the tap
 * then awaits `resolve`), otherwise the product's groups or "unavailable".
 */
export function useProductOptions(branchId: string) {
  const qc = useQueryClient()
  const tree = useQuery({
    queryKey: optionTreeKey(branchId),
    queryFn: () => fetchOptionTree(branchId),
    enabled: branchId !== "",
    staleTime: CATALOG_STALE_MS,
  })

  const optionsFor = useCallback(
    (productId: string): OptionLookup | undefined =>
      tree.data ? optionGroupsFrom(tree.data.get(productId)) : undefined,
    [tree.data],
  )

  const resolve = useCallback(
    async (productId: string): Promise<OptionLookup> => {
      const data = await qc.fetchQuery({
        queryKey: optionTreeKey(branchId),
        queryFn: () => fetchOptionTree(branchId),
        staleTime: CATALOG_STALE_MS,
      })
      return optionGroupsFrom(data.get(productId))
    },
    [qc, branchId],
  )

  return { optionsFor, resolve }
}

/** Drops every cached catalog answer — used after a price_mismatch. */
export function useRefreshOrderCatalog() {
  const qc = useQueryClient()
  return useCallback(() => qc.invalidateQueries({ queryKey: ["pos-order"] }), [qc])
}

/** Open checks of a branch with their totals — shown on occupied table tiles. */
export function useOpenChecks(branchId: string) {
  return useQuery({
    queryKey: ["checks", { status: "open", branch_id: branchId }],
    queryFn: async () => {
      const { data } = await api.get<Check[]>("/api/v1/pos/checks", { params: { status: "open" } })
      return (data ?? []).filter((c) => c.status === "open" && c.branch_id === branchId)
    },
    enabled: branchId !== "",
    refetchInterval: 30_000,
  })
}

function invalidateFloor(qc: ReturnType<typeof useQueryClient>) {
  void qc.invalidateQueries({ queryKey: ["pos-tables"] })
  void qc.invalidateQueries({ queryKey: ["checks"] })
}

// Opening a check is NOT idempotency-gated on the backend; a double submit is
// harmless anyway because the second one is refused with table_occupied.
export function useOpenTableCheck() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: { branch_id: string; table_id: string; table_label: string }) => {
      const { data } = await api.post<Check>("/api/v1/pos/checks", body)
      return data
    },
    onSettled: () => invalidateFloor(qc),
  })
}

// POST /pos/orders requires Idempotency-Key (ADR-SEC-003). The caller owns
// the key's lifecycle (lib/pos-order.ts submissionKeyFor): a retry must resend
// the same key with the same body.
export function usePlaceTableOrder() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ body, idempotencyKey }: { body: PlaceOrderBody; idempotencyKey: string }) => {
      const { data } = await api.post<Order>("/api/v1/pos/orders", body, {
        headers: { "Idempotency-Key": idempotencyKey },
      })
      return data
    },
    onSuccess: () => invalidateFloor(qc),
  })
}

export function useCleanTable() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (tableId: string) => {
      await api.post(`/api/v1/pos/tables/${tableId}/status`, { status: "empty" })
    },
    onSettled: () => invalidateFloor(qc),
  })
}
