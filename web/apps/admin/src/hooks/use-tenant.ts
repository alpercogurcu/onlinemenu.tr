import { useQuery } from "@tanstack/react-query"

import api from "@/lib/api"
import type { Branch, Tenant } from "@/types"

export function useBranches(tenantId: string) {
  return useQuery({
    queryKey: ["tenants", tenantId, "branches"],
    queryFn: async () => {
      const { data } = await api.get<Branch[]>(`/tenants/${tenantId}/branches/`)
      return data ?? []
    },
    enabled: tenantId !== "",
  })
}

export function useTenant(tenantId: string) {
  return useQuery({
    queryKey: ["tenants", tenantId],
    queryFn: async () => {
      const { data } = await api.get<Tenant>(`/tenants/${tenantId}/`)
      return data
    },
    enabled: tenantId !== "",
  })
}
