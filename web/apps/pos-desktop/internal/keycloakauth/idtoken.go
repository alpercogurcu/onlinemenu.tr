package keycloakauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// DecodeNonce extracts the `nonce` claim from an ID token's payload segment
// WITHOUT verifying its signature.
//
// This is safe here for the same reason apiclient.Client.claims() skips
// verification of its own CTX token: idToken only ever originates from this
// process's own just-completed Client.Exchange call over TLS (never from an
// untrusted external source), and it is used purely as a replay-protection
// heuristic — comparing against the nonce this same process generated for
// this specific login attempt (see pkce.go's GenerateNonce) — never as an
// authorization decision. The actual authorization boundary is the
// backend's own signature-verified Keycloak access token check on every
// subsequent API call (KeycloakVerifier — see
// backend/internal/platform/auth/keycloak_verifier.go).
func DecodeNonce(idToken string) (string, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", errors.New("keycloakauth: malformed id_token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("keycloakauth: decode id_token payload: %w", err)
	}
	var claims struct {
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("keycloakauth: unmarshal id_token claims: %w", err)
	}
	return claims.Nonce, nil
}
