package auth

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// SessionValidator answers "is this cash session still open" for
// RequireOpenSession. It is defined here, at the point of use, rather than
// in payment/public, so platform/auth never imports a business module —
// payment's concrete implementation satisfies this interface structurally
// (see internal/modules/payment.CashSessionOpenChecker), matching this
// repo's "define interfaces where they are used" convention.
type SessionValidator interface {
	// IsOpen reports whether sessionID (scoped to tenantID) is still open.
	// A returned error is treated as "not open" by RequireOpenSession —
	// ADR-AUTH-001 item 10's fail-closed default applies here too.
	IsOpen(ctx context.Context, tenantID, sessionID uuid.UUID) (bool, error)
}

// RequireOpenSession is global middleware (mounted after Middleware) that
// enforces ADR-DATA-008 PIN akışı §4: a context token minted by
// IssueStaffForSession carries a session_id claim (Principal.SessionID), and
// the server must reject it once that session is no longer open. Closing the
// drawer therefore invalidates every token derived from it, even though the
// token itself is a stateless, still-cryptographically-valid, unexpired JWT
// — there is no revocation list, so this live per-request check is the
// entire mechanism, not the token's exp field.
//
// Requests whose principal carries SessionID == uuid.Nil (every token from
// the normal Keycloak /auth/context flow, and every pre-context/customer
// principal) skip the check entirely — that is the common case, and it costs
// nothing.
//
// validator may be nil: cmd/api is the only binary today that wires both
// identity and payment, so it is the only one that can supply a real
// SessionValidator. cmd/api-core/api-pos/api-finance mount this same
// middleware with validator == nil. Because all four binaries share one CTX
// signing secret, a session-scoped token minted by cmd/api remains
// cryptographically valid on those other binaries — but with no validator to
// ask, a session-scoped token there is refused outright rather than trusted
// blindly (fail-closed, ADR-AUTH-001 item 10). Do not "fix" this by treating
// a nil validator as "allow": that would let a closed session's token keep
// working forever on any binary that happens not to wire payment.
func RequireOpenSession(validator SessionValidator, logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, err := FromContext(r.Context())
			if err != nil || principal.SessionID == uuid.Nil {
				next.ServeHTTP(w, r)
				return
			}

			if validator == nil {
				logger.Warn("auth: session-scoped token seen but no SessionValidator wired, refusing",
					zap.String("session_id", principal.SessionID.String()))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			open, err := validator.IsOpen(r.Context(), principal.TenantID, principal.SessionID)
			if err != nil {
				logger.Warn("auth: session liveness check failed, denying by default",
					zap.String("session_id", principal.SessionID.String()), zap.Error(err))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if !open {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
