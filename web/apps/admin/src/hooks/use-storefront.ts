import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import api from "@/lib/api"
import type { IssuedQRCode, QRCode } from "@/types"

const QR_CODES_KEY = "storefront-qr-codes"

export function useQRCodes(branchId: string) {
  return useQuery({
    queryKey: [QR_CODES_KEY, branchId],
    queryFn: async () => {
      const { data } = await api.get<QRCode[]>("/api/v1/storefront/qr-codes", {
        params: { branch_id: branchId },
      })
      return data ?? []
    },
    enabled: branchId !== "",
  })
}

// The create/rotate mutations return the raw token. They deliberately do NOT
// write it into the query cache (no setQueryData): the cache outlives the
// dialog and is inspectable from the devtools panel, and the token is the only
// secret protecting a table's ordering session. It reaches the caller as the
// mutateAsync return value and nowhere else. Callers must also reset() the
// mutation when they are done, so it does not linger in mutation state either.
export function useCreateQRCode() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: { branch_id: string; table_id: string; table_label: string }) => {
      const { data } = await api.post<IssuedQRCode>("/api/v1/storefront/qr-codes", body)
      return data
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: [QR_CODES_KEY] })
    },
  })
}

export function useRevokeQRCode() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      const { data } = await api.post<QRCode>(`/api/v1/storefront/qr-codes/${id}/revoke`)
      return data
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: [QR_CODES_KEY] })
    },
  })
}

export function useRotateQRCode() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      const { data } = await api.post<IssuedQRCode>(`/api/v1/storefront/qr-codes/${id}/rotate`)
      return data
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: [QR_CODES_KEY] })
    },
  })
}
