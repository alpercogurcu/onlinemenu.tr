package keycloakauth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestGenerateVerifier_UniqueAndCorrectLength(t *testing.T) {
	v1, err := GenerateVerifier()
	if err != nil {
		t.Fatalf("GenerateVerifier: %v", err)
	}
	v2, err := GenerateVerifier()
	if err != nil {
		t.Fatalf("GenerateVerifier: %v", err)
	}
	if v1 == v2 {
		t.Fatal("two calls to GenerateVerifier produced the same value")
	}
	// RFC 7636 §4.1: 43-128 characters.
	if len(v1) < 43 || len(v1) > 128 {
		t.Fatalf("verifier length = %d, want 43-128", len(v1))
	}
}

func TestChallenge_MatchesS256Spec(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])

	got := Challenge(verifier)
	if got != want {
		t.Fatalf("Challenge = %q, want %q", got, want)
	}
}

func TestChallenge_IsDeterministic(t *testing.T) {
	verifier, err := GenerateVerifier()
	if err != nil {
		t.Fatalf("GenerateVerifier: %v", err)
	}
	if Challenge(verifier) != Challenge(verifier) {
		t.Fatal("Challenge is not deterministic for the same verifier")
	}
}

func TestGenerateState_And_GenerateNonce_AreUniqueAndNonEmpty(t *testing.T) {
	s1, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	s2, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	if s1 == "" || s1 == s2 {
		t.Fatalf("GenerateState produced empty or repeated values: %q, %q", s1, s2)
	}

	n1, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce: %v", err)
	}
	n2, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce: %v", err)
	}
	if n1 == "" || n1 == n2 {
		t.Fatalf("GenerateNonce produced empty or repeated values: %q, %q", n1, n2)
	}
}
