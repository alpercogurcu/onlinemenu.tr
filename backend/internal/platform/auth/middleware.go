package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// Claims represents the minimal set of JWT claims the platform requires.
// Full validation (signature, expiry) must be performed by the TokenVerifier
// before this struct is populated.
type Claims struct {
	Sub      string   `json:"sub"`
	TenantID string   `json:"tenant_id"`
	Roles    []string `json:"roles"`

	// BranchIDs may be absent for tenant-admin tokens (unrestricted access).
	BranchIDs []string `json:"branch_ids"`
}

// TokenVerifier validates a raw JWT and extracts its claims.
// Implemented by the Keycloak JWKS verifier in production and by test stubs.
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (*Claims, error)
}

// Middleware validates the Bearer JWT and stores a Principal in the request context.
// Cryptographic verification is delegated to the TokenVerifier implementation.
func Middleware(verifier TokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, err := extractBearerToken(r)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			claims, err := verifier.Verify(r.Context(), raw)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			principal, err := claimsToPrincipal(claims)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
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

func claimsToPrincipal(c *Claims) (Principal, error) {
	if c.Sub == "" {
		return Principal{}, errors.New("auth: missing sub claim")
	}

	tenantID, err := uuid.Parse(c.TenantID)
	if err != nil {
		return Principal{}, fmt.Errorf("auth: invalid tenant_id claim: %w", err)
	}

	branchIDs := make([]uuid.UUID, 0, len(c.BranchIDs))
	for _, raw := range c.BranchIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return Principal{}, fmt.Errorf("auth: invalid branch_id %q: %w", raw, err)
		}
		branchIDs = append(branchIDs, id)
	}

	return Principal{
		Sub:       c.Sub,
		TenantID:  tenantID,
		BranchIDs: branchIDs,
		Roles:     c.Roles,
	}, nil
}
