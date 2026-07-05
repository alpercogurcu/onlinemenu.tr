// Pre-context identity bootstrap calls (ADR-AUTH-001 steps 1-3: Keycloak JWT
// -> /me/contexts -> /auth/context -> CTX token). These use a raw fetch with
// an explicit bearer token and deliberately bypass the shared `api` axios
// instance/interceptor: at this point in the flow there is either no CTX
// token yet (initial login) or the caller needs to authenticate with the
// Keycloak access token specifically, not whatever the interceptor would
// inject (see me_handler.go — /me/contexts and /auth/context accept a
// pre-context Keycloak-verified Principal).
import type { Me, TenantContext, TenantContextListResponse } from "@/types"

const API_BASE = process.env.NEXT_PUBLIC_API_CORE_URL ?? "/api/core"

async function bearerFetch<T>(path: string, token: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      ...init?.headers,
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
  })
  if (!res.ok) {
    throw new Error(`${path} failed with status ${res.status}`)
  }
  return (await res.json()) as T
}

export async function fetchContexts(keycloakAccessToken: string): Promise<TenantContext[]> {
  const data = await bearerFetch<TenantContextListResponse>(
    "/v1/identity/me/contexts",
    keycloakAccessToken,
  )
  return data.contexts
}

export async function selectMembershipContext(
  keycloakAccessToken: string,
  membershipId: string,
): Promise<string> {
  const data = await bearerFetch<{ token: string }>("/v1/identity/auth/context", keycloakAccessToken, {
    method: "POST",
    body: JSON.stringify({ membership_id: membershipId }),
  })
  return data.token
}

export async function fetchMe(ctxToken: string): Promise<Me> {
  return bearerFetch<Me>("/v1/identity/me", ctxToken)
}
