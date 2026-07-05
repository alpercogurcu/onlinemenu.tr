import { create } from "zustand"

import { clearAccessToken, setAccessToken } from "@/lib/api"
import { clearKeycloakTokens, getKeycloakTokens } from "@/lib/keycloak-token-store"

interface User {
  id: string
  name: string
  email: string
}

interface AuthState {
  // Access token lives only in api.ts (in-memory), not here.
  // This store holds the non-sensitive user identity for UI rendering.
  user: User | null
  tenantId: string | null
  setSession: (token: string, user: User, tenantId: string) => void
  logout: () => void
}

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  tenantId: null,
  setSession: (token, user, tenantId) => {
    setAccessToken(token)
    set({ user, tenantId })
  },
  logout: () => {
    clearAccessToken()
    clearKeycloakTokens()
    set({ user: null, tenantId: null })
  },
}))

// Whether the current session originated from the Keycloak PKCE flow (vs.
// the dev-login shortcut). Callers use this to decide between a Keycloak
// end_session redirect and a plain client-side logout.
export function hasKeycloakSession(): boolean {
  return getKeycloakTokens() !== null
}
