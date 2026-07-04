package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testIssuer   = "https://keycloak.test/realms/onlinemenu"
	testAudience = "onlinemenu-api"
	testKid      = "test-kid-1"
)

// newTestJWKSServer starts an httptest server that serves the JWKS document for
// the given RSA public key under testKid, and returns the server plus config
// pointing a KeycloakVerifier at it.
func newTestJWKSServer(t *testing.T, key *rsa.PublicKey) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/protocol/openid-connect/certs", func(w http.ResponseWriter, _ *http.Request) {
		doc := jwksDocument{Keys: []jwk{{
			Kty: "RSA",
			Kid: testKid,
			Alg: "RS256",
			Use: "sig",
			N:   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			E:   base64.RawURLEncoding.EncodeToString(big64(key.E)),
		}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
	return httptest.NewServer(mux)
}

// big64 encodes a small int (the RSA public exponent, e.g. 65537) as minimal big-endian bytes.
func big64(e int) []byte {
	if e == 0 {
		return []byte{0}
	}
	var b []byte
	for e > 0 {
		b = append([]byte{byte(e & 0xff)}, b...)
		e >>= 8
	}
	return b
}

func signToken(t *testing.T, priv *rsa.PrivateKey, kid string, claims jwt.MapClaims, method jwt.SigningMethod) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(priv)
	require.NoError(t, err)
	return signed
}

func validClaims() jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss": testIssuer,
		"aud": testAudience,
		"sub": "keycloak-sub-uuid",
		"iat": now.Unix(),
		"nbf": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
}

func TestKeycloakVerifier_Verify(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := newTestJWKSServer(t, &priv.PublicKey)
	defer srv.Close()

	cfg := KeycloakVerifierConfig{
		IssuerURL: testIssuer,
		JWKSURL:   srv.URL + "/protocol/openid-connect/certs",
		Audience:  testAudience,
	}

	verifier, err := NewKeycloakVerifier(context.Background(), cfg)
	require.NoError(t, err)

	t.Run("valid_token_accepted", func(t *testing.T) {
		tok := signToken(t, priv, testKid, validClaims(), jwt.SigningMethodRS256)
		claims, err := verifier.Verify(context.Background(), tok)
		require.NoError(t, err)
		assert.Equal(t, "keycloak-sub-uuid", claims.Sub)
	})

	t.Run("expired_token_rejected", func(t *testing.T) {
		claims := validClaims()
		claims["exp"] = time.Now().Add(-time.Hour).Unix()
		tok := signToken(t, priv, testKid, claims, jwt.SigningMethodRS256)
		_, err := verifier.Verify(context.Background(), tok)
		assert.Error(t, err)
	})

	t.Run("wrong_algorithm_rejected", func(t *testing.T) {
		// "none" algorithm must never be accepted regardless of claim content.
		noneToken := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims())
		noneToken.Header["kid"] = testKid
		signed, err := noneToken.SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)
		_, err = verifier.Verify(context.Background(), signed)
		assert.Error(t, err)

		// HS256 using the RSA modulus bytes as an HMAC secret must also fail
		// (classic RS256->HS256 alg confusion attack using the public key as the HMAC secret).
		hsToken := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims())
		hsToken.Header["kid"] = testKid
		hsSigned, err := hsToken.SignedString(priv.PublicKey.N.Bytes())
		require.NoError(t, err)
		_, err = verifier.Verify(context.Background(), hsSigned)
		assert.Error(t, err)
	})

	t.Run("wrong_issuer_rejected", func(t *testing.T) {
		claims := validClaims()
		claims["iss"] = "https://evil.example/realms/other"
		tok := signToken(t, priv, testKid, claims, jwt.SigningMethodRS256)
		_, err := verifier.Verify(context.Background(), tok)
		assert.Error(t, err)
	})

	t.Run("wrong_audience_rejected", func(t *testing.T) {
		claims := validClaims()
		claims["aud"] = "some-other-client"
		tok := signToken(t, priv, testKid, claims, jwt.SigningMethodRS256)
		_, err := verifier.Verify(context.Background(), tok)
		assert.Error(t, err)
	})

	t.Run("unknown_kid_rejected", func(t *testing.T) {
		tok := signToken(t, priv, "no-such-kid", validClaims(), jwt.SigningMethodRS256)
		_, err := verifier.Verify(context.Background(), tok)
		assert.Error(t, err)
	})

	t.Run("not_before_in_future_rejected", func(t *testing.T) {
		claims := validClaims()
		claims["nbf"] = time.Now().Add(time.Hour).Unix()
		tok := signToken(t, priv, testKid, claims, jwt.SigningMethodRS256)
		_, err := verifier.Verify(context.Background(), tok)
		assert.Error(t, err)
	})
}

func TestNewKeycloakVerifier_RequiresConfig(t *testing.T) {
	_, err := NewKeycloakVerifier(context.Background(), KeycloakVerifierConfig{Audience: "x"})
	assert.Error(t, err)

	_, err = NewKeycloakVerifier(context.Background(), KeycloakVerifierConfig{IssuerURL: "https://x"})
	assert.Error(t, err)
}

func TestNewKeycloakVerifier_UnreachableJWKS(t *testing.T) {
	_, err := NewKeycloakVerifier(context.Background(), KeycloakVerifierConfig{
		IssuerURL: testIssuer,
		JWKSURL:   "http://127.0.0.1:1/certs",
		Audience:  testAudience,
	})
	assert.Error(t, err)
}
