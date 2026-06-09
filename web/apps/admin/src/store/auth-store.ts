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
  setSession: (token: string, user: User) => void
  logout: () => void
}

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  setSession: (token, user) => {
    setAccessToken(token)
    set({ user })
  },
  logout: () => {
    clearAccessToken()
    set({ user: null })
  },
}))
