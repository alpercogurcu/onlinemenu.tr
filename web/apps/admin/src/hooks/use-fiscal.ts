import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import axios from "axios"

import api from "@/lib/api"
import type { BasketMode, FiscalSection, FiscalSectionMapping, FiscalTerminal } from "@/types"

// The fiscal admin API (backend/internal/modules/payment/http/fiscal_handler.go)
// is developed in parallel and may not be routed yet in a given environment.
// GET/list endpoints degrade to an empty result on a 404 so the page renders
// its normal empty state instead of an error screen — a real 4xx/5xx (e.g. the
// 422 branch_id-required guard) still throws so isError surfaces as usual.
async function listOr404Empty<T>(request: Promise<{ data: T }>): Promise<T> {
  try {
    const { data } = await request
    return data
  } catch (err) {
    if (axios.isAxiosError(err) && err.response?.status === 404) {
      return [] as unknown as T
    }
    throw err
  }
}

// ─── Terminals ──────────────────────────────────────────────────────────────

export function useFiscalTerminals(branchId: string) {
  return useQuery({
    queryKey: ["fiscal-terminals", branchId],
    queryFn: () =>
      listOr404Empty<FiscalTerminal[]>(
        api.get<FiscalTerminal[]>("/api/v1/fiscal/terminals", {
          params: { branch_id: branchId },
        }),
      ),
    enabled: Boolean(branchId),
  })
}

export interface CreateFiscalTerminalBody {
  qr: string
  branch_id: string
  label: string
  basket_mode: BasketMode
}

export function useCreateFiscalTerminal() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateFiscalTerminalBody) =>
      api.post<FiscalTerminal>("/api/v1/fiscal/terminals", body),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: ["fiscal-terminals", variables.branch_id] })
    },
  })
}

export interface UpdateFiscalTerminalBody {
  id: string
  // Not sent to the backend (PATCH only accepts label/basket_mode/is_active)
  // — kept here so the mutation can invalidate the right branch-scoped cache.
  branch_id: string
  label?: string
  basket_mode?: BasketMode
  is_active?: boolean
}

export function useUpdateFiscalTerminal() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (vars: UpdateFiscalTerminalBody) =>
      api.patch<FiscalTerminal>(`/api/v1/fiscal/terminals/${vars.id}`, {
        label: vars.label,
        basket_mode: vars.basket_mode,
        is_active: vars.is_active,
      }),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: ["fiscal-terminals", variables.branch_id] })
    },
  })
}

// sync-sections pulls the device's current VAT sections and replaces the
// stored copy (full replacement, per ADR-FISCAL-002 §2 — never a partial
// merge, so a removed device section cannot silently linger as mappable).
export function useSyncFiscalSections() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (terminalId: string) =>
      api.post<FiscalSection[]>(`/api/v1/fiscal/terminals/${terminalId}/sync-sections`),
    onSuccess: (_data, terminalId) => {
      void qc.invalidateQueries({ queryKey: ["fiscal-sections", terminalId] })
    },
  })
}

export function useFiscalSections(terminalId: string) {
  return useQuery({
    queryKey: ["fiscal-sections", terminalId],
    queryFn: () =>
      listOr404Empty<FiscalSection[]>(
        api.get<FiscalSection[]>(`/api/v1/fiscal/terminals/${terminalId}/sections`),
      ),
    enabled: Boolean(terminalId),
  })
}

// ─── Section mappings ───────────────────────────────────────────────────────

export function useFiscalSectionMappings(branchId: string) {
  return useQuery({
    queryKey: ["fiscal-section-mappings", branchId],
    queryFn: () =>
      listOr404Empty<FiscalSectionMapping[]>(
        api.get<FiscalSectionMapping[]>("/api/v1/fiscal/section-mappings", {
          params: { branch_id: branchId },
        }),
      ),
    enabled: Boolean(branchId),
  })
}

export function useReplaceFiscalSectionMappings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { branch_id: string; mappings: FiscalSectionMapping[] }) =>
      api.put<FiscalSectionMapping[]>("/api/v1/fiscal/section-mappings", body),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: ["fiscal-section-mappings", variables.branch_id] })
    },
  })
}
