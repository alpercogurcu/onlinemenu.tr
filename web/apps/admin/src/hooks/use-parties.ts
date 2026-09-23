import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import api from "@/lib/api"
import type { Party } from "@/types"

interface PartyListResponse {
  parties: Party[]
}

// `enabled` lets a screen shared with non-manager roles (the warehouse role
// on inventory pages) skip a request that can only answer 403: party.party.read
// is manager-only.
export function useParties(
  params?: { limit?: number; offset?: number; type?: string },
  options?: { enabled?: boolean },
) {
  return useQuery({
    queryKey: ["parties", params],
    queryFn: async () => {
      const { data } = await api.get<PartyListResponse>("/api/v1/parties/", { params })
      return data?.parties ?? []
    },
    enabled: options?.enabled ?? true,
  })
}

export function useParty(id: string) {
  return useQuery({
    queryKey: ["parties", id],
    queryFn: async () => {
      const { data } = await api.get<Party>(`/api/v1/parties/${id}`)
      return data
    },
    enabled: Boolean(id),
  })
}

export function useSearchParties(q: string) {
  return useQuery({
    queryKey: ["parties", "search", q],
    queryFn: async () => {
      const { data } = await api.get<PartyListResponse>("/api/v1/parties/search", { params: { q } })
      return data?.parties ?? []
    },
    enabled: q.length >= 2,
  })
}

export function useCreateParty() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Partial<Party>) => api.post<Party>("/api/v1/parties/", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["parties"] })
    },
  })
}

export function useUpdateParty() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }: Partial<Party> & { id: string }) =>
      api.put<Party>(`/api/v1/parties/${id}`, body),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: ["parties"] })
      void qc.invalidateQueries({ queryKey: ["parties", variables.id] })
    },
  })
}
