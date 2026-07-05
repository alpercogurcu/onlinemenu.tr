import { useQuery } from "@tanstack/react-query"

import api from "@/lib/api"
import type { Me, TenantContextListResponse } from "@/types"

export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: async () => {
      const { data } = await api.get<Me>("/v1/identity/me")
      return data
    },
  })
}

// Lists the memberships available to the current CTX session (used for an
// in-app "switch tenant/branch" affordance, distinct from the pre-context
// login bootstrap in lib/identity-bootstrap.ts, which authenticates with the
// Keycloak access token instead of a CTX token).
export function useTenantContexts() {
  return useQuery({
    queryKey: ["me", "contexts"],
    queryFn: async () => {
      const { data } = await api.get<TenantContextListResponse>("/v1/identity/me/contexts")
      return data.contexts
    },
  })
}
