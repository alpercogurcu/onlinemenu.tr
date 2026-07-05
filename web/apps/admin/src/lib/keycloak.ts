// Client-side Keycloak OIDC config for the `admin-panel` public client
// (Authorization Code + PKCE, ADR-AUTH-002 / deploy/keycloak/realm-onlinemenu.json).
//
// This file is imported by client components (login page, callback page) and
// must only ever use NEXT_PUBLIC_ env vars. The actual token exchange happens
// server-side — see keycloak-server.ts, imported exclusively by route
// handlers under src/app/api/auth/**.

export const KEYCLOAK_URL = process.env.NEXT_PUBLIC_KEYCLOAK_URL ?? "http://localhost:8090"
export const KEYCLOAK_REALM = process.env.NEXT_PUBLIC_KEYCLOAK_REALM ?? "onlinemenu"
export const KEYCLOAK_CLIENT_ID = "admin-panel"

const PKCE_VERIFIER_KEY = "om_pkce_verifier"
const PKCE_STATE_KEY = "om_pkce_state"
const PKCE_NONCE_KEY = "om_pkce_nonce"

export function authorizationEndpoint(): string {
  return `${KEYCLOAK_URL}/realms/${KEYCLOAK_REALM}/protocol/openid-connect/auth`
}

export function endSessionEndpoint(): string {
  return `${KEYCLOAK_URL}/realms/${KEYCLOAK_REALM}/protocol/openid-connect/logout`
}

/** Redirect URI registered on the `admin-panel` client (`http://localhost:3000/*` in dev). */
export function callbackRedirectUri(): string {
  return `${window.location.origin}/auth/callback`
}

export interface AuthorizeUrlParams {
  redirectUri: string
  state: string
  nonce: string
  codeChallenge: string
}

export function buildAuthorizeUrl(params: AuthorizeUrlParams): string {
  const query = new URLSearchParams({
    client_id: KEYCLOAK_CLIENT_ID,
    response_type: "code",
    scope: "openid",
    redirect_uri: params.redirectUri,
    state: params.state,
    nonce: params.nonce,
    code_challenge: params.codeChallenge,
    code_challenge_method: "S256",
  })
  return `${authorizationEndpoint()}?${query.toString()}`
}

export function buildLogoutUrl(postLogoutRedirectUri: string, idTokenHint?: string): string {
  const query = new URLSearchParams({
    client_id: KEYCLOAK_CLIENT_ID,
    post_logout_redirect_uri: postLogoutRedirectUri,
  })
  if (idTokenHint) {
    query.set("id_token_hint", idTokenHint)
  }
  return `${endSessionEndpoint()}?${query.toString()}`
}

export interface PkceParams {
  verifier: string
  state: string
  nonce: string
}

// Transient PKCE material must survive the full-page redirect to Keycloak, so
// it cannot live in the in-memory token holders (api.ts / keycloak-token-store.ts).
// sessionStorage is safe here: it never holds the access/refresh token itself,
// only single-use verifier/state/nonce values, and they are deleted as soon
// as the callback consumes them (consumePkceParams is a one-shot read).
export function savePkceParams(params: PkceParams): void {
  sessionStorage.setItem(PKCE_VERIFIER_KEY, params.verifier)
  sessionStorage.setItem(PKCE_STATE_KEY, params.state)
  sessionStorage.setItem(PKCE_NONCE_KEY, params.nonce)
}

export function consumePkceParams(): PkceParams | null {
  const verifier = sessionStorage.getItem(PKCE_VERIFIER_KEY)
  const state = sessionStorage.getItem(PKCE_STATE_KEY)
  const nonce = sessionStorage.getItem(PKCE_NONCE_KEY)

  sessionStorage.removeItem(PKCE_VERIFIER_KEY)
  sessionStorage.removeItem(PKCE_STATE_KEY)
  sessionStorage.removeItem(PKCE_NONCE_KEY)

  if (!verifier || !state || !nonce) return null
  return { verifier, state, nonce }
}
