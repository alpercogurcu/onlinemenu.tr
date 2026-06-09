import { useQuery } from "@tanstack/react-query"

import api from "@/lib/api"
import type { Me, TenantContext } from "@/types"

export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: async () => {
      const { data } = await api.get<Me>("/v1/identity/me")
      return data
    },
  })
}

export function useTenantContexts() {
  return useQuery({
    queryKey: ["me", "contexts"],
    queryFn: async () => {
      const { data } = await api.get<TenantContext[]>("/v1/identity/me/contexts")
      return data
    },
  })
}
