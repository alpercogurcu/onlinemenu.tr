package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// jwksRefreshInterval bounds how long a fetched JWKS document is trusted before
// a background refresh is attempted. Individual Verify calls never block on a
// network round trip once a key set has been loaded at least once.
const jwksRefreshInterval = 10 * time.Minute

// jwksFetchTimeout bounds a single JWKS HTTP fetch.
const jwksFetchTimeout = 5 * time.Second

// jwk is a single JSON Web Key as returned by Keycloak's certs endpoint.
// Only the RSA fields required to reconstruct an *rsa.PublicKey are modeled.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

// KeycloakVerifierConfig configures a KeycloakVerifier. Populated from env vars
// by the fx provider in cmd/api/main.go — never read directly by module code.
type KeycloakVerifierConfig struct {
	// IssuerURL is the expected `iss` claim, e.g. https://keycloak.example.com/realms/onlinemenu.
	IssuerURL string
	// JWKSURL is the Keycloak certs endpoint. Defaults to IssuerURL + "/protocol/openid-connect/certs"
	// when empty.
	JWKSURL string
	// Audience is the expected `aud` claim (the Keycloak client ID).
	Audience string
}

// KeycloakVerifier validates Keycloak-issued RS256 JWTs against the realm's JWKS.
// Keys are cached by kid and refreshed in the background; an unknown kid triggers
// a synchronous refresh so key rotation does not cause spurious verification failures.
type KeycloakVerifier struct {
	issuer   string
	jwksURL  string
	audience string
	client   *http.Client

	mu          sync.RWMutex
	keys        map[string]*rsa.PublicKey
	lastFetched time.Time
}

// NewKeycloakVerifier constructs a KeycloakVerifier. It performs an initial JWKS
// fetch so startup fails fast if Keycloak is unreachable or misconfigured.
func NewKeycloakVerifier(ctx context.Context, cfg KeycloakVerifierConfig) (*KeycloakVerifier, error) {
	if cfg.IssuerURL == "" {
		return nil, errors.New("auth: KeycloakVerifierConfig.IssuerURL is required")
	}
	if cfg.Audience == "" {
		return nil, errors.New("auth: KeycloakVerifierConfig.Audience is required")
	}
	jwksURL := cfg.JWKSURL
	if jwksURL == "" {
		jwksURL = cfg.IssuerURL + "/protocol/openid-connect/certs"
	}

	v := &KeycloakVerifier{
		issuer:   cfg.IssuerURL,
		jwksURL:  jwksURL,
		audience: cfg.Audience,
		client:   &http.Client{Timeout: jwksFetchTimeout},
		keys:     make(map[string]*rsa.PublicKey),
	}

	if err := v.refresh(ctx); err != nil {
		return nil, fmt.Errorf("auth: initial JWKS fetch: %w", err)
	}
	return v, nil
}

// Verify validates the raw JWT's signature (RS256 only — alg confusion is rejected
// by construction, since jwt.WithValidMethods restricts accepted algorithms before
// any key lookup happens), issuer, audience, and standard time claims, then extracts
// the KeycloakClaims required by the platform.
func (v *KeycloakVerifier) Verify(ctx context.Context, rawToken string) (*KeycloakClaims, error) {
	var claims jwt.MapClaims

	token, err := jwt.ParseWithClaims(rawToken, &claims, func(t *jwt.Token) (interface{}, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok || kid == "" {
			return nil, errors.New("auth: JWT missing kid header")
		}
		key, ok := v.lookupKey(kid)
		if !ok {
			// Unknown kid: refresh once synchronously in case of key rotation, then retry.
			if refreshErr := v.refresh(ctx); refreshErr != nil {
				return nil, fmt.Errorf("auth: refresh JWKS after unknown kid: %w", refreshErr)
			}
			key, ok = v.lookupKey(kid)
			if !ok {
				return nil, fmt.Errorf("auth: unknown JWKS kid %q", kid)
			}
		}
		return key, nil
	},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("auth: verify keycloak token: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("auth: keycloak token invalid")
	}

	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return nil, errors.New("auth: keycloak token missing sub claim")
	}

	v.maybeBackgroundRefresh(ctx)

	return &KeycloakClaims{Sub: sub}, nil
}

func (v *KeycloakVerifier) lookupKey(kid string) (*rsa.PublicKey, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	k, ok := v.keys[kid]
	return k, ok
}

// maybeBackgroundRefresh kicks off an async JWKS refresh once the cached document
// is older than jwksRefreshInterval. Verify never blocks on this.
func (v *KeycloakVerifier) maybeBackgroundRefresh(ctx context.Context) {
	v.mu.RLock()
	stale := time.Since(v.lastFetched) > jwksRefreshInterval
	v.mu.RUnlock()
	if !stale {
		return
	}
	go func() {
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), jwksFetchTimeout)
		defer cancel()
		_ = v.refresh(refreshCtx)
	}()
}

// refresh fetches and parses the JWKS document, replacing the in-memory key cache.
func (v *KeycloakVerifier) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("auth: build JWKS request: %w", err)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("auth: fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("auth: JWKS endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var doc jwksDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("auth: decode JWKS document: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		pub, err := rsaPublicKeyFromJWK(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return errors.New("auth: JWKS document contains no usable RSA signing keys")
	}

	v.mu.Lock()
	v.keys = keys
	v.lastFetched = time.Now()
	v.mu.Unlock()

	return nil
}

// rsaPublicKeyFromJWK reconstructs an *rsa.PublicKey from the base64url-encoded
// modulus (n) and exponent (e) fields of an RSA JWK, per RFC 7517 §9.3.
func rsaPublicKeyFromJWK(nEnc, eEnc string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nEnc)
	if err != nil {
		return nil, fmt.Errorf("auth: decode JWK modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eEnc)
	if err != nil {
		return nil, fmt.Errorf("auth: decode JWK exponent: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() {
		return nil, errors.New("auth: JWK exponent out of range")
	}

	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}
