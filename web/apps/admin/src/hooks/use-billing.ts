import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import api from "@/lib/api"
import type { Invoice } from "@/types"

interface InvoiceListResponse {
  invoices: Invoice[]
}

export function useInvoices(params?: { limit?: number; offset?: number }) {
  return useQuery({
    queryKey: ["invoices", params],
    queryFn: async () => {
      const { data } = await api.get<InvoiceListResponse>("/api/v1/invoices/", { params })
      return data?.invoices ?? []
    },
  })
}

export function useInvoice(id: string) {
  return useQuery({
    queryKey: ["invoices", id],
    queryFn: async () => {
      const { data } = await api.get<Invoice>(`/api/v1/invoices/${id}`)
      return data
    },
    enabled: Boolean(id),
  })
}

export function useRetryInvoice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.post(`/api/v1/invoices/${id}/retry`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["invoices"] })
    },
  })
}
