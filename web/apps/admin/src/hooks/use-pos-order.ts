import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query"
import { useCallback } from "react"

import api from "@/lib/api"
import {
  optionGroupsFrom,
  sellableProducts,
  type OptionLookup,
  type PlaceOrderBody,
  type ProductOptionsWire,
} from "@/lib/pos-order"
import type { Category, Check, Order, OrderStatus, Product, WaiterCategoryLayout } from "@/types"

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

interface PosBranchSettingsWire {
  waiter_category_layout?: string
}

// Branch settings change rarely (an admin flips them once); longer than the
// catalog's staleTime so navigating table -> order -> table never refetches.
const BRANCH_SETTINGS_STALE_MS = 5 * 60_000

/**
 * The branch's category layout for the waiter order screen
 * (GET /pos/branch-settings, permission pos.check.read — every waiter has it).
 *
 * Fail-open by design: the layout is cosmetic, so a missing endpoint (it ships
 * separately from this screen), a network error, an absent row or an unknown
 * value all fall back to the default top chips instead of surfacing an error.
 * Read once when the screen opens; branchId in the key refetches on branch
 * switch.
 */
export function useWaiterCategoryLayout(branchId: string): WaiterCategoryLayout {
  const query = useQuery({
    queryKey: ["pos-order", "branch-settings", branchId],
    queryFn: async (): Promise<WaiterCategoryLayout> => {
      try {
        const { data } = await api.get<PosBranchSettingsWire>("/api/v1/pos/branch-settings", {
          params: { branch_id: branchId },
        })
        return data?.waiter_category_layout === "side" ? "side" : "top"
      } catch {
        return "top"
      }
    },
    enabled: branchId !== "",
    staleTime: BRANCH_SETTINGS_STALE_MS,
  })
  return query.data ?? "top"
}

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

export interface OpenServiceCheckBody {
  branch_id: string
  service_type: "takeaway" | "delivery"
  customer_name: string
  customer_phone?: string
  customer_address?: string
}

// A tableless check (gel al / paket): no table_id — the backend derives the
// KDS/ticket label from customer_name. Optional fields are only sent when
// filled so the backend never stores empty strings over its defaults.
export function useOpenServiceCheck() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: OpenServiceCheckBody) => {
      const { data } = await api.post<Check>("/api/v1/pos/checks", body)
      return data
    },
    onSettled: () => invalidateFloor(qc),
  })
}

// Rejected/cancelled orders are off the bill (same rule as SentItems), so the
// picker row's "N ürün" matches what the customer will actually receive.
const NOT_BILLED: ReadonlySet<OrderStatus> = new Set(["rejected", "cancelled"])

/**
 * Item quantities of the given checks, for the gel al / paket rows
 * ("Alper Vural — 5 ürün · ₺940"). One request per check, but the query key
 * is shared with useCheckOrders so an order screen visit fills this cache and
 * vice versa; open takeaway/delivery checks are few at any moment.
 */
export function useCheckItemCounts(checkIds: string[]): Map<string, number> {
  const results = useQueries({
    queries: checkIds.map((id) => ({
      queryKey: ["checks", id, "orders"] as const,
      queryFn: async () => {
        const { data } = await api.get<Order[]>(`/api/v1/pos/checks/${id}/orders`)
        return data ?? []
      },
      staleTime: 30_000,
    })),
  })
  const counts = new Map<string, number>()
  results.forEach((result, i) => {
    if (!result.data) return
    const count = result.data
      .filter((order) => !NOT_BILLED.has(order.status))
      .reduce((sum, order) => sum + order.items.reduce((s, item) => s + item.quantity, 0), 0)
    counts.set(checkIds[i], count)
  })
  return counts
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
