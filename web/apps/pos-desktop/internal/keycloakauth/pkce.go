// Package keycloakauth implements the POS desktop client's half of the
// RFC 8252 (OAuth 2.0 for Native Apps) loopback-redirect Authorization Code
// + PKCE flow against the onlinemenu.tr Keycloak realm's "pos-desktop"
// public client (see deploy/keycloak/realm-onlinemenu.json and
// deploy/keycloak/README.md, "Client'lar").
//
// This package talks to the Keycloak IdP only (authorize/token/end_session
// endpoints) — it never calls the onlinemenu.tr backend. Backend calls that
// are part of the login sequence (GET /v1/identity/me/contexts, POST
// /v1/identity/auth/context) go exclusively through apiclient.Client, the
// process's single backend HTTP authority (see pos-desktop/README.md, "Tek
// token-refresh otoritesi"). main.App is the only code that talks to both
// packages and stitches the sequence together.
package keycloakauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// generateRandomURLSafe returns n bytes of CSPRNG output, base64url-encoded
// without padding — the format RFC 7636 §4.1 requires for a PKCE
// code_verifier, and a convenient, sufficiently-random format for state and
// nonce too (they have no format requirement beyond "hard to guess").
func generateRandomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("keycloakauth: read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// GenerateVerifier returns a fresh PKCE code_verifier: 32 CSPRNG bytes,
// base64url-encoded to a 43-character string — within RFC 7636's required
// 43-128 character range. Callers must keep this in memory only for the
// duration of one login attempt and never persist it to disk (see
// pos-desktop's task brief, "verifier bellekte kalır").
func GenerateVerifier() (string, error) {
	return generateRandomURLSafe(32)
}

// Challenge derives the S256 PKCE code_challenge from verifier:
// base64url(sha256(verifier)), no padding (RFC 7636 §4.2).
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// GenerateState returns a CSPRNG state value for CSRF/callback-binding
// protection (RFC 6749 §10.12) — the loopback callback handler rejects any
// request whose state does not match this value.
func GenerateState() (string, error) {
	return generateRandomURLSafe(24)
}

// GenerateNonce returns a CSPRNG nonce for OIDC ID token replay protection.
// The caller verifies it against the id_token's `nonce` claim after
// exchange (see DecodeNonce) — this binds the token to this specific login
// attempt, not just this specific authorization code.
func GenerateNonce() (string, error) {
	return generateRandomURLSafe(24)
}
