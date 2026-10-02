import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import api from "@/lib/api"
import type { PosBranchSettings, PosOrderFlow, RoundingStepMinor, WaiterCategoryLayout } from "@/types"

const SETTINGS_KEY = "pos-branch-settings"
const SETTINGS_PATH = "/api/v1/pos/branch-settings"

export const posBranchSettingsKey = (branchId: string) => [SETTINGS_KEY, branchId] as const

// `enabled` lets the caller gate the fetch on permission: the preferences
// section is only rendered for pos.table.manage, so a role without it never
// fires the GET (same contract as useStorefrontSettings).
export function usePosBranchSettings(branchId: string, opts?: { enabled?: boolean }) {
  return useQuery({
    queryKey: posBranchSettingsKey(branchId),
    queryFn: async () => {
      const { data } = await api.get<PosBranchSettings>(SETTINGS_PATH, {
        params: { branch_id: branchId },
      })
      return data
    },
    enabled: branchId !== "" && (opts?.enabled ?? true),
  })
}

// Partial on purpose: the PUT carries only the field the owner changed, so two
// radio groups edited in quick succession cannot overwrite each other with a
// stale sibling value.
export interface PosBranchSettingsInput {
  branch_id: string
  waiter_category_layout?: WaiterCategoryLayout
  order_flow?: PosOrderFlow
  rounding_cash_enabled?: boolean
  rounding_card_enabled?: boolean
  rounding_step_minor?: RoundingStepMinor
  rounding_max_per_check_minor?: number
}

export function useUpdatePosBranchSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: PosBranchSettingsInput) => {
      const { data } = await api.put<PosBranchSettings>(SETTINGS_PATH, body)
      return data
    },
    onSuccess: (data) => {
      // Seed the cache with the PUT response so the radios move immediately,
      // then invalidate so the next render reconciles with the server.
      qc.setQueryData(posBranchSettingsKey(data.branch_id), data)
      void qc.invalidateQueries({ queryKey: posBranchSettingsKey(data.branch_id) })
      // Garson sipariş ekranı yerleşimi kendi anahtarıyla cache'ler
      // (use-pos-order.ts, fail-open gerekçesiyle ayrık) — ayar değişince
      // aynı cihazdaki sipariş ekranı da tazelensin.
      void qc.invalidateQueries({ queryKey: ["pos-order", "branch-settings"] })
    },
  })
}
