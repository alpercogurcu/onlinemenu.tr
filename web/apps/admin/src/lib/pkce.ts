// PKCE (RFC 7636) helpers for the Keycloak Authorization Code + PKCE flow
// used by the `admin-panel` public client (no client_secret). Kept as pure
// functions so the crypto primitives are unit-testable, including against
// the RFC 7636 Appendix B.1 test vector (see pkce.test.ts).

function base64UrlEncode(bytes: Uint8Array): string {
  let binary = ""
  for (const byte of bytes) {
    binary += String.fromCharCode(byte)
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
}

function randomBytes(length: number): Uint8Array {
  const bytes = new Uint8Array(length)
  crypto.getRandomValues(bytes)
  return bytes
}

/**
 * RFC 7636 `code_verifier`: a 43-128 char unreserved-character string.
 * 32 random bytes base64url-encode to exactly 43 characters.
 */
export function generateCodeVerifier(): string {
  return base64UrlEncode(randomBytes(32))
}

/**
 * S256 `code_challenge` derived from a `code_verifier` (RFC 7636 §4.2):
 * BASE64URL(SHA256(ASCII(code_verifier))).
 */
export async function generateCodeChallenge(verifier: string): Promise<string> {
  const data = new TextEncoder().encode(verifier)
  const digest = await crypto.subtle.digest("SHA-256", data)
  return base64UrlEncode(new Uint8Array(digest))
}

/** Opaque random token for the OAuth `state` param (CSRF protection). */
export function generateState(): string {
  return base64UrlEncode(randomBytes(16))
}

/** Opaque random token for the OIDC `nonce` param (id_token replay protection). */
export function generateNonce(): string {
  return base64UrlEncode(randomBytes(16))
}
