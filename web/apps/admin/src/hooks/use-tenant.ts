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

// Projected endpoint (GET /tenants/{id}/modules), readable by every staff
// role — unlike useTenant's GET /tenants/{id}/, which is manager-only
// (tenant.tenant.read) and 403s for cashier/waiter/kitchen/etc. The sidebar
// needs the tenant's enabled_modules for every role, not just managers.
export function useTenantModules(tenantId: string) {
  return useQuery({
    queryKey: ["tenants", tenantId, "modules"],
    queryFn: async () => {
      const { data } = await api.get<{ enabled_modules: string[] }>(`/tenants/${tenantId}/modules`)
      return data
    },
    enabled: tenantId !== "",
  })
}
