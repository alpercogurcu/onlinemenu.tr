import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import api from "@/lib/api"
import type { Me, MembershipListResponse, MembershipStatus, TenantContextListResponse } from "@/types"

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

export function useMemberships(tenantId: string) {
  return useQuery({
    queryKey: ["memberships", tenantId],
    queryFn: async () => {
      const { data } = await api.get<MembershipListResponse>(`/v1/identity/${tenantId}/memberships`)
      return data.memberships
    },
    enabled: tenantId !== "",
  })
}

// PUT /memberships/{id} only carries the status (see
// updateMembershipStatusRequest in membership_handler.go) — there is no
// role/branch change endpoint yet, so the UI must not offer one.
export function useUpdateMembershipStatus(tenantId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ membershipId, status }: { membershipId: string; status: MembershipStatus }) => {
      await api.put(`/v1/identity/${tenantId}/memberships/${membershipId}`, { status })
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["memberships", tenantId] })
    },
  })
}
