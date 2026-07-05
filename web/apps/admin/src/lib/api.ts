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

// Exposed read-only so callers that cannot go through the axios instance
// (e.g. a raw `fetch` to the kitchen-stream bridge route, which needs to set
// the Authorization header itself) can reuse the same in-memory token
// without duplicating its storage.
export function getAccessToken(): string | null {
  return inMemoryToken
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

// 401 interceptor: only redirect when a session token was present (expiry),
// not on login failures. Also guarded against server-side execution.
api.interceptors.response.use(
  (response) => response,
  (error: unknown) => {
    if (
      typeof window !== "undefined" &&
      axios.isAxiosError(error) &&
      error.response?.status === 401 &&
      inMemoryToken !== null
    ) {
      clearAccessToken()
      window.location.href = "/login"
    }
    return Promise.reject(error)
  },
)

export default api
