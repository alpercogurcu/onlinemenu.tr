import axios from "axios"

// Token is kept in memory only — never persisted to localStorage/sessionStorage.
// XSS cannot exfiltrate an in-memory variable. The refresh token lives in an
// HttpOnly, Secure, SameSite=Lax cookie set by the backend on /auth/login.
let inMemoryToken: string | null = null

export function setAccessToken(token: string | null) {
  inMemoryToken = token
}

export function clearAccessToken() {
  inMemoryToken = null
}

const api = axios.create({
  baseURL: process.env.NEXT_PUBLIC_API_CORE_URL ?? "/api/core",
  headers: { "Content-Type": "application/json" },
  // Sends the HttpOnly refresh-token cookie on same-origin requests.
  withCredentials: true,
})

api.interceptors.request.use((config) => {
  if (inMemoryToken) {
    config.headers.Authorization = `Bearer ${inMemoryToken}`
  }
  return config
})

export default api
