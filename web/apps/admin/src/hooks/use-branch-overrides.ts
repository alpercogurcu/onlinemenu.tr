import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query"

import { overrideDiffers } from "@/lib/branch-overrides"
import api from "@/lib/api"
import type { Branch, BranchProductOverride } from "@/types"

export const branchOverridesKey = (branchId: string) => ["branch-overrides", branchId] as const

const listPath = (branchId: string) => `/api/v1/catalog/branches/${branchId}/product-overrides`
const rowPath = (branchId: string, productId: string) =>
  `/api/v1/catalog/branches/${branchId}/products/${productId}/override`

async function fetchOverrides(branchId: string): Promise<BranchProductOverride[]> {
  const { data } = await api.get<BranchProductOverride[]>(listPath(branchId))
  return data ?? []
}

// `enabled` lets a caller that is not allowed to read overrides (they are
// answered 403 for any role that cannot see the branch) keep the query off.
export function useBranchOverrides(branchId: string, enabled = true) {
  return useQuery({
    queryKey: branchOverridesKey(branchId),
    queryFn: () => fetchOverrides(branchId),
    enabled: Boolean(branchId) && enabled,
  })
}

// How many branches sell `productId` differently from the tenant catalog.
// Shares each branch's query key with useBranchOverrides, so opening the
// pricing screen afterwards costs no extra request.
export function useProductBranchOverrideBranches(
  productId: string,
  branches: Branch[],
  enabled: boolean,
): { branchIds: string[]; isLoading: boolean } {
  const results = useQueries({
    queries: branches.map((branch) => ({
      queryKey: branchOverridesKey(branch.id),
      queryFn: () => fetchOverrides(branch.id),
      enabled: enabled && Boolean(productId),
    })),
  })

  const branchIds = branches
    .filter((_, i) => overrideDiffers(results[i]?.data?.find((row) => row.product_id === productId)))
    .map((branch) => branch.id)

  return { branchIds, isLoading: results.some((r) => r.isLoading) }
}

// Both fields are required on purpose: the backend defaults an omitted
// is_available to true, so a price-only body would silently re-open a product
// the owner had closed. Same failure class as useUpdateProduct's full-replace
// body — a missing field is a compile error here, not a data-loss bug.
export interface BranchOverrideInput {
  productId: string
  is_available: boolean
  price_amount: number | null
}

interface OptimisticContext {
  previous: BranchProductOverride | undefined
}

function withoutRow(rows: BranchProductOverride[], productId: string) {
  return rows.filter((row) => row.product_id !== productId)
}

// Optimistic on a per-row basis: the rollback restores only the row this
// mutation touched, so a failure never rewinds a neighbouring edit that is
// still in flight. The list is refetched once the last in-flight mutation
// settles (an earlier refetch would overwrite a later optimistic row with a
// response that predates it).
export function useUpsertBranchOverride(branchId: string) {
  const qc = useQueryClient()
  const key = branchOverridesKey(branchId)
  const mutationKey = ["branch-overrides", branchId, "mutate"] as const
  // One queue per branch: edits to the same row (price, then the switch) hit
  // the server in the order the owner made them, and delete/put never race.
  const scopeId = `branch-overrides:${branchId}`

  return useMutation<BranchProductOverride, unknown, BranchOverrideInput, OptimisticContext>({
    mutationKey,
    scope: { id: scopeId },
    mutationFn: async ({ productId, is_available, price_amount }) => {
      const { data } = await api.put<BranchProductOverride>(rowPath(branchId, productId), {
        is_available,
        price_amount,
      })
      return data
    },
    onMutate: async ({ productId, is_available, price_amount }) => {
      await qc.cancelQueries({ queryKey: key })
      const rows = qc.getQueryData<BranchProductOverride[]>(key) ?? []
      const previous = rows.find((row) => row.product_id === productId)
      const next: BranchProductOverride = {
        branch_id: branchId,
        product_id: productId,
        is_available,
        price_amount,
        updated_at: previous?.updated_at ?? new Date().toISOString(),
      }
      qc.setQueryData<BranchProductOverride[]>(key, [...withoutRow(rows, productId), next])
      return { previous }
    },
    onError: (_error, { productId }, context) => {
      const rows = qc.getQueryData<BranchProductOverride[]>(key) ?? []
      const restored = withoutRow(rows, productId)
      qc.setQueryData<BranchProductOverride[]>(key, context?.previous ? [...restored, context.previous] : restored)
    },
    onSettled: () => {
      if (qc.isMutating({ mutationKey: ["branch-overrides", branchId] }) <= 1) {
        void qc.invalidateQueries({ queryKey: key })
      }
    },
  })
}

// "Varsayılana dön": removes the row entirely (price AND availability), which
// is not the same as a PUT with an empty price (that keeps is_available).
export function useDeleteBranchOverride(branchId: string) {
  const qc = useQueryClient()
  const key = branchOverridesKey(branchId)

  return useMutation<unknown, unknown, { productId: string }, OptimisticContext>({
    mutationKey: ["branch-overrides", branchId, "delete"],
    scope: { id: `branch-overrides:${branchId}` },
    mutationFn: ({ productId }) => api.delete(rowPath(branchId, productId)),
    onMutate: async ({ productId }) => {
      await qc.cancelQueries({ queryKey: key })
      const rows = qc.getQueryData<BranchProductOverride[]>(key) ?? []
      const previous = rows.find((row) => row.product_id === productId)
      qc.setQueryData<BranchProductOverride[]>(key, withoutRow(rows, productId))
      return { previous }
    },
    onError: (_error, _vars, context) => {
      if (!context?.previous) return
      const rows = qc.getQueryData<BranchProductOverride[]>(key) ?? []
      qc.setQueryData<BranchProductOverride[]>(key, [...withoutRow(rows, context.previous.product_id), context.previous])
    },
    onSettled: () => {
      if (qc.isMutating({ mutationKey: ["branch-overrides", branchId] }) <= 1) {
        void qc.invalidateQueries({ queryKey: key })
      }
    },
  })
}
