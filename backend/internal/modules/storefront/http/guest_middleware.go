package http

import (
	"net/http"
	"time"

	"go.uber.org/zap"

	"onlinemenu.tr/internal/platform/auth"
)

const (
	// guestCookieName is the only place a guest session travels. It is never
	// accepted from an Authorization header: that header belongs to staff
	// tokens, and letting one endpoint read a guest identity from it would
	// invite the mirror mistake on a staff endpoint.
	guestCookieName = "om_guest"

	// guestCookiePath scopes the cookie to the public API. A cookie sent to
	// /api/v1/* would be attached to every staff request the same browser
	// makes, for no reason and with real cross-surface risk.
	guestCookiePath = "/api/public/v1"
)

// setGuestCookie writes the session cookie for a freshly resolved QR scan.
//
// HttpOnly: the menu app never needs to read the token from JS, and a stored
// XSS on a public page must not be able to exfiltrate a session.
// SameSite=Lax: the storefront is navigated to from a QR scan (a top-level
// GET), which Lax allows, while cross-site POSTs do not carry the cookie.
// Secure: on in every environment except dev — see Config.CookieSecure, which
// is set from the composition root, not sniffed from the request (a proxy
// that forgets X-Forwarded-Proto must not silently downgrade the cookie).
func setGuestCookie(w http.ResponseWriter, token string, expiresAt time.Time, secure bool) {
	// #nosec G124 -- Secure is a parameter fed from Config.CookieSecure at the
	// composition root; gosec only accepts a literal true and cannot see that.
	// Narrowed to this call rather than excluded repo-wide, so the same rule
	// keeps guarding every other cookie in the codebase.
	http.SetCookie(w, &http.Cookie{
		Name:     guestCookieName,
		Value:    token,
		Path:     guestCookiePath,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// requireGuestSession is the public surface's entire authentication chain.
//
// auth.FromContext is never called here, and no auth.Principal is ever put
// into the context: a guest is not a principal, the two types do not convert,
// and nothing downstream of this middleware can therefore be handed to OPA or
// to a permission check. A missing, malformed, expired or wrongly-signed
// cookie is 401 in every case — the reasons are not distinguished to the
// caller, because telling an anonymous prober "that token was real but
// expired" is free information.
func (h *PublicHandler) requireGuestSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(guestCookieName)
		if err != nil || cookie.Value == "" {
			writeProblem(w, http.StatusUnauthorized, codeUnauthorized,
				"Oturum bulunamadı. Lütfen masanızdaki QR kodu tekrar okutun.")
			return
		}

		session, err := h.signer.VerifyGuest(cookie.Value)
		if err != nil {
			h.logger.Debug("storefront: guest token rejected", zap.Error(err))
			writeProblem(w, http.StatusUnauthorized, codeUnauthorized,
				"Oturumunuz geçersiz veya süresi dolmuş. Lütfen QR kodu tekrar okutun.")
			return
		}

		next.ServeHTTP(w, r.WithContext(auth.WithGuestSession(r.Context(), session)))
	})
}

// guestFromRequest returns the session the middleware above established.
// A missing session here means the route was mounted outside the guarded
// group — a wiring bug, so it fails closed with 401 rather than proceeding
// with a zero-valued session (whose uuid.Nil tenant would then be refused by
// db.WithTenantTx anyway, but much later and far less legibly).
func guestFromRequest(w http.ResponseWriter, r *http.Request) (auth.GuestSession, bool) {
	session, ok := auth.GuestFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusUnauthorized, codeUnauthorized,
			"Oturum bulunamadı. Lütfen masanızdaki QR kodu tekrar okutun.")
		return auth.GuestSession{}, false
	}
	return session, true
}
