import axios, { type InternalAxiosRequestConfig } from "axios"

import { selectMembershipContext } from "@/lib/identity-bootstrap"
import {
  clearKeycloakTokens,
  getKeycloakTokens,
  getSelectedMembershipId,
  isKeycloakAccessTokenValid,
  setKeycloakTokens,
  tokensFromResponse,
} from "@/lib/keycloak-token-store"

// CTX token is kept in memory only — never persisted to localStorage/
// sessionStorage. XSS cannot exfiltrate an in-memory variable. On a page
// refresh it is gone and the user falls back to /login — accepted trade-off,
// same as the Keycloak access/refresh tokens (see keycloak-token-store.ts).
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
  withCredentials: true,
})

api.interceptors.request.use((config) => {
  if (inMemoryToken) {
    config.headers.Authorization = `Bearer ${inMemoryToken}`
  }
  return config
})

async function refreshKeycloakAccessToken(): Promise<string | null> {
  const kc = getKeycloakTokens()
  if (!kc) return null

  try {
    const res = await fetch("/api/auth/refresh", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refresh_token: kc.refreshToken }),
    })
    if (!res.ok) return null

    const tokens = tokensFromResponse(await res.json())
    setKeycloakTokens(tokens)
    return tokens.accessToken
  } catch {
    return null
  }
}

// Re-derives a CTX token from the still-valid (or freshly refreshed)
// Keycloak session, without sending the user back through the context
// picker (POST /v1/identity/auth/context only needs the membership_id and a
// live Keycloak access token — see identity-bootstrap.ts). Single-flighted
// so N parallel CTX-401s trigger exactly one refresh/re-select.
let ctxRecoveryPromise: Promise<string | null> | null = null

async function recoverCtxToken(): Promise<string | null> {
  const membershipId = getSelectedMembershipId()
  if (!membershipId) return null // dev-login session (no Keycloak tokens) — nothing to recover from

  let keycloakAccessToken = isKeycloakAccessTokenValid()
    ? (getKeycloakTokens()?.accessToken ?? null)
    : await refreshKeycloakAccessToken()

  if (!keycloakAccessToken) return null

  try {
    const ctxToken = await selectMembershipContext(keycloakAccessToken, membershipId)
    setAccessToken(ctxToken)
    return ctxToken
  } catch {
    return null
  }
}

interface RetriableRequestConfig extends InternalAxiosRequestConfig {
  _ctxRetry?: boolean
}

// 401 interceptor: only act when a session token was present (expiry), not on
// login failures. Also guarded against server-side execution.
//
// CTX token expired (accessTokenLifespan=300s on the platform-signed token) ->
// try to silently recover a fresh one from the Keycloak session and retry the
// original request once; only fall back to /login if that recovery fails
// (Keycloak refresh token also expired/invalid, or no Keycloak session at all).
api.interceptors.response.use(
  (response) => response,
  async (error: unknown) => {
    if (typeof window === "undefined" || !axios.isAxiosError(error)) {
      return Promise.reject(error)
    }

    const isUnauthorized = error.response?.status === 401
    const config = error.config as RetriableRequestConfig | undefined

    if (isUnauthorized && inMemoryToken !== null && config && !config._ctxRetry) {
      config._ctxRetry = true
      if (!ctxRecoveryPromise) {
        ctxRecoveryPromise = recoverCtxToken().finally(() => {
          ctxRecoveryPromise = null
        })
      }
      const recoveredToken = await ctxRecoveryPromise
      if (recoveredToken) {
        return api(config)
      }
    }

    if (isUnauthorized && inMemoryToken !== null) {
      clearAccessToken()
      clearKeycloakTokens()
      window.location.href = "/login"
    }

    return Promise.reject(error)
  },
)

export default api
