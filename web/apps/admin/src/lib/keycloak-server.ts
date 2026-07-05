// Server-only Keycloak token endpoint calls (authorization_code exchange +
// refresh_token). Imported exclusively by Next.js Route Handlers under
// src/app/api/auth/** — never import this from a client component.
//
// Why a route handler instead of a pure-browser exchange (what oidc-client-ts
// or a naive callback page would do): the `admin-panel` client scopes are
// `basic` + `onlinemenu-audience` only (see deploy/keycloak/README.md, "Wave 2
// notu"). The `web-origins` scope that makes Keycloak emit CORS headers is
// NOT attached to this client, so the browser cannot reliably call the token
// endpoint directly. Doing the exchange here (browser -> same-origin route
// handler -> Keycloak, server-to-server) sidesteps that without touching the
// realm config, which is out of scope for this change.
//
// This is still a PKCE-only, secret-less exchange (public client) — this
// module is not a "confidential client" backend-for-frontend.

const KEYCLOAK_URL =
  process.env.KEYCLOAK_INTERNAL_URL ?? process.env.NEXT_PUBLIC_KEYCLOAK_URL ?? "http://localhost:8090"
const KEYCLOAK_REALM = process.env.NEXT_PUBLIC_KEYCLOAK_REALM ?? "onlinemenu"
const KEYCLOAK_CLIENT_ID = "admin-panel"

function tokenEndpoint(): string {
  return `${KEYCLOAK_URL}/realms/${KEYCLOAK_REALM}/protocol/openid-connect/token`
}

export interface KeycloakTokenResponse {
  access_token: string
  refresh_token: string
  expires_in: number
  id_token?: string
  token_type: string
}

export async function exchangeAuthorizationCode(params: {
  code: string
  codeVerifier: string
  redirectUri: string
}): Promise<KeycloakTokenResponse> {
  const body = new URLSearchParams({
    grant_type: "authorization_code",
    client_id: KEYCLOAK_CLIENT_ID,
    code: params.code,
    code_verifier: params.codeVerifier,
    redirect_uri: params.redirectUri,
  })
  return postToken(body)
}

export async function refreshAccessToken(refreshToken: string): Promise<KeycloakTokenResponse> {
  const body = new URLSearchParams({
    grant_type: "refresh_token",
    client_id: KEYCLOAK_CLIENT_ID,
    refresh_token: refreshToken,
  })
  return postToken(body)
}

async function postToken(body: URLSearchParams): Promise<KeycloakTokenResponse> {
  const res = await fetch(tokenEndpoint(), {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body,
    cache: "no-store",
  })
  if (!res.ok) {
    const text = await res.text().catch(() => "")
    throw new Error(`keycloak token endpoint responded ${res.status}: ${text}`)
  }
  return (await res.json()) as KeycloakTokenResponse
}
