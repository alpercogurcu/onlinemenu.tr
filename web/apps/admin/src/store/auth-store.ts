import { create } from "zustand"

import { clearAccessToken, setAccessToken } from "@/lib/api"

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
    set({ user: null, tenantId: null })
  },
}))
