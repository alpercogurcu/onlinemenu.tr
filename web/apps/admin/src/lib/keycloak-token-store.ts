// In-memory Keycloak token holder — mirrors the pattern already used for the
// CTX token in lib/api.ts: tokens are never persisted to localStorage/
// sessionStorage (XSS cannot exfiltrate an in-memory variable). Lost on page
// refresh by design; this is the same accepted trade-off as the existing
// dev-login flow (refresh -> back to /login).
export interface KeycloakTokens {
  accessToken: string
  refreshToken: string
  idToken?: string
  expiresAt: number // epoch ms
}

let tokens: KeycloakTokens | null = null
let selectedMembershipId: string | null = null

export function setKeycloakTokens(t: KeycloakTokens): void {
  tokens = t
}

export function getKeycloakTokens(): KeycloakTokens | null {
  return tokens
}

export function getKeycloakIdToken(): string | null {
  return tokens?.idToken ?? null
}

export function clearKeycloakTokens(): void {
  tokens = null
  selectedMembershipId = null
}

/**
 * The membership_id last posted to POST /v1/identity/auth/context. Needed so
 * a CTX-401 (access token expired, e.g. after accessTokenLifespan=300s) can
 * silently re-derive a fresh CTX token from the still-valid Keycloak session
 * without sending the user back through the context picker.
 */
export function setSelectedMembershipId(id: string): void {
  selectedMembershipId = id
}

export function getSelectedMembershipId(): string | null {
  return selectedMembershipId
}

const EXPIRY_SKEW_MS = 10_000

export function isKeycloakAccessTokenValid(): boolean {
  return tokens !== null && Date.now() < tokens.expiresAt - EXPIRY_SKEW_MS
}

export function tokensFromResponse(res: {
  access_token: string
  refresh_token: string
  expires_in: number
  id_token?: string
}): KeycloakTokens {
  return {
    accessToken: res.access_token,
    refreshToken: res.refresh_token,
    idToken: res.id_token,
    expiresAt: Date.now() + res.expires_in * 1000,
  }
}
