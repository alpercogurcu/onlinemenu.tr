package auth

import (
	"context"
	"net/http"

	"go.uber.org/zap"
)

// scopeContextKey is the context key under which the OPA-derived Scope is stored
// after a successful RequirePermission check.
type scopeContextKey struct{}

var scopeKey = scopeContextKey{}

// RequirePermission returns chi-compatible middleware that authorizes the given
// action against the request's Principal via the OPA Engine (ADR-AUTH-001, layer 2).
//
// On deny (or on any evaluation error — fail-closed per ADR-AUTH-001 item 10), the
// response is a bare 403 with no distinguishing detail, to avoid leaking whether a
// resource exists to a caller who lacks access to it.
//
// On allow, the resolved Decision.Scope is stored in the request context for the
// service layer to translate into a WHERE clause (ADR-AUTH-001, layer 3). It is
// retrieved via ScopeFromContext.
func RequirePermission(engine *Engine, action string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, err := FromContext(r.Context())
			if err != nil {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			decision, err := engine.Decide(r.Context(), action, principal)
			if err != nil {
				engine.logger.Warn("authz: policy evaluation failed, denying by default",
					zap.String("action", action), zap.Error(err))
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if !decision.Allow {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			ctx := context.WithValue(r.Context(), scopeKey, decision.Scope)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ScopeFromContext retrieves the OPA-derived scope ("tenant" | "branch" | "own")
// stored by RequirePermission. Returns false when no scope has been resolved
// (e.g. the route has no RequirePermission middleware).
func ScopeFromContext(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(scopeKey).(string)
	return s, ok
}
