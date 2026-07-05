package keycloakauth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func buildFakeIDToken(t *testing.T, nonce string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]string{"nonce": nonce, "sub": "keycloak-sub-1"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".unverified-signature"
}

func TestDecodeNonce_ExtractsClaim(t *testing.T) {
	idToken := buildFakeIDToken(t, "nonce-xyz")

	got, err := DecodeNonce(idToken)
	if err != nil {
		t.Fatalf("DecodeNonce: %v", err)
	}
	if got != "nonce-xyz" {
		t.Fatalf("DecodeNonce = %q, want nonce-xyz", got)
	}
}

func TestDecodeNonce_MalformedToken(t *testing.T) {
	if _, err := DecodeNonce("not-a-jwt"); err == nil {
		t.Fatal("DecodeNonce: want error for malformed token, got nil")
	}
}

func TestDecodeNonce_InvalidBase64Payload(t *testing.T) {
	if _, err := DecodeNonce("header.not!base64url.sig"); err == nil {
		t.Fatal("DecodeNonce: want error for invalid base64 payload, got nil")
	}
}
