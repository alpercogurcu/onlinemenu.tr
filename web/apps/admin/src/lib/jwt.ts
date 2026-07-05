// Minimal JWT payload decode — NOT signature verification. Used only to read
// the `nonce` claim out of Keycloak's id_token for OIDC replay protection in
// the browser. Signature/aud/exp verification of the access token used for
// authorization happens server-side (backend/internal/platform/auth/keycloak_verifier.go);
// this module must never be used to make an authorization decision.
export function decodeJwtPayload<T = Record<string, unknown>>(token: string): T | null {
  const parts = token.split(".")
  if (parts.length !== 3) return null

  try {
    const base64 = parts[1].replace(/-/g, "+").replace(/_/g, "/")
    const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), "=")
    const json = atob(padded)
    return JSON.parse(json) as T
  } catch {
    return null
  }
}
