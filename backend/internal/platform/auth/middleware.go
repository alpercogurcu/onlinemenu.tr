package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// KeycloakClaims represents the minimal set of Keycloak JWT claims the platform requires.
// Full validation (signature, expiry) must be performed by the TokenVerifier
// before this struct is populated.
// After Keycloak authentication, the caller must exchange for a context token via
// POST /identity/auth/context to obtain a Principal with role and branch context.
type KeycloakClaims struct {
	Sub string `json:"sub"` // Keycloak subject — maps to persons.keycloak_sub
}

// TokenVerifier validates a raw Keycloak JWT and extracts its claims.
// Implemented by the Keycloak JWKS verifier in production and by test stubs.
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (*KeycloakClaims, error)
}

// Middleware validates the Bearer token and stores a Principal in the request context.
//
// Token routing:
//   - CTX tokens (platform-signed, Typ=CTX) → verified by ContextTokenSigner
//   - All other tokens → delegated to the Keycloak TokenVerifier
//
// CTX tokens carry full role+branch context and are required for all resource endpoints.
// Keycloak tokens are only valid for the context-listing and context-selection endpoints.
func Middleware(verifier TokenVerifier, signer *ContextTokenSigner) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, err := extractBearerToken(r)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			var principal Principal

			if IsContextToken(raw) {
				principal, err = signer.Verify(raw)
				if err != nil {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
			} else {
				claims, err := verifier.Verify(r.Context(), raw)
				if err != nil {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				principal, err = keycloakClaimsToPrincipal(claims)
				if err != nil {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
			}

			ctx := context.WithValue(r.Context(), principalKey, principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// FromContext retrieves the Principal stored by the auth middleware.
// Returns an error if the context does not contain a principal.
func FromContext(ctx context.Context) (Principal, error) {
	p, ok := ctx.Value(principalKey).(Principal)
	if !ok {
		return Principal{}, errors.New("auth: principal not found in context")
	}
	return p, nil
}

func extractBearerToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", errors.New("auth: missing Authorization header")
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", errors.New("auth: invalid Authorization header format")
	}
	return parts[1], nil
}

// keycloakClaimsToPrincipal converts Keycloak claims into a pre-context Principal.
// PersonID is uuid.Nil; the identity service resolves it from KeycloakSub.
// This principal is only valid for /identity/me/contexts and /identity/auth/context.
func keycloakClaimsToPrincipal(c *KeycloakClaims) (Principal, error) {
	if c.Sub == "" {
		return Principal{}, errors.New("auth: missing sub claim")
	}
	return Principal{KeycloakSub: c.Sub}, nil
}
