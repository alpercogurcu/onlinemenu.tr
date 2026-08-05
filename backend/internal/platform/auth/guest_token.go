package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	guestTokenIssuer   = "onlinemenu"
	guestTokenAudience = "storefront-guest"
	guestTokenTTL      = 4 * time.Hour

	// guestTokenTyp is deliberately NOT "CTX". IsContextToken therefore
	// returns false for a guest token, so Middleware routes it down the
	// Keycloak path, where it fails signature verification and the request is
	// rejected with 401. "A guest token is worthless on a staff endpoint" is
	// thus a structural property of the token format, not a rule some
	// handler has to remember to enforce.
	guestTokenTyp = "GUEST"
)

var (
	// ErrGuestTokenExpired is returned when a guest token's exp has passed.
	ErrGuestTokenExpired = errors.New("auth: guest token expired")
	// ErrGuestTokenInvalid is returned for any malformed, wrongly-typed,
	// wrongly-audienced or badly-signed guest token.
	ErrGuestTokenInvalid = errors.New("auth: guest token invalid")
)

// guestTokenHeader is the fixed header embedded in every guest token.
type guestTokenHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// guestTokenClaims is the payload signed into a guest token.
//
// There is no subject/person and no role list: a guest is anonymous by
// definition, and giving this struct a PersonID or RoleIDs field would invite
// code that converts it into a Principal.
type guestTokenClaims struct {
	Iss      string `json:"iss"`
	Aud      string `json:"aud"`
	TenantID string `json:"tid"`
	BranchID string `json:"bid"`
	TableID  string `json:"table_id"`
	QRCodeID string `json:"qr"`
	// Sid is a per-session random UUID, minted when the QR token is
	// exchanged. It is what storefront_guest_orders is keyed on, so a guest
	// can only ever read back orders its own session placed.
	SessionID string `json:"sid"`
	Exp       int64  `json:"exp"`
}

// GuestSession is the decoded identity of an anonymous QR diner.
//
// It is intentionally NOT an auth.Principal and carries no PersonID or roles:
// nothing in the OPA/permission chain can be fed a guest, and no staff
// handler can accidentally accept one, because the types do not convert.
type GuestSession struct {
	TenantID  uuid.UUID
	BranchID  uuid.UUID
	TableID   uuid.UUID
	QRCodeID  uuid.UUID
	SessionID uuid.UUID
	ExpiresAt time.Time
}

// GuestTokenSigner issues and verifies storefront guest session tokens.
//
// It is a separate type from ContextTokenSigner, holding a separate secret
// (STOREFRONT_GUEST_TOKEN_SECRET), so that compromising the public QR surface
// cannot be used to mint staff tokens, and so no one can add a guest-issuing
// method to the staff signer.
type GuestTokenSigner struct {
	secret []byte
}

// NewGuestTokenSigner constructs a signer from a raw secret.
// The secret must be at least 32 bytes (enforced at startup via fx).
func NewGuestTokenSigner(secret []byte) (*GuestTokenSigner, error) {
	if len(secret) < 32 {
		return nil, errors.New("auth: guest token secret must be at least 32 bytes")
	}
	return &GuestTokenSigner{secret: secret}, nil
}

// IssueGuest creates a guest session token for a validated QR code.
// sessionID must be freshly random per session — see guestTokenClaims.Sid.
func (s *GuestTokenSigner) IssueGuest(tenantID, branchID, tableID, qrCodeID, sessionID uuid.UUID) (string, error) {
	return s.sign(guestTokenClaims{
		Iss:       guestTokenIssuer,
		Aud:       guestTokenAudience,
		TenantID:  tenantID.String(),
		BranchID:  branchID.String(),
		TableID:   tableID.String(),
		QRCodeID:  qrCodeID.String(),
		SessionID: sessionID.String(),
		Exp:       time.Now().Add(guestTokenTTL).Unix(),
	})
}

// VerifyGuest parses and validates a guest token, returning the GuestSession.
// A valid staff context (CTX) token is rejected here just as firmly as a
// forgery: typ and aud are checked explicitly, before and after the signature.
func (s *GuestTokenSigner) VerifyGuest(raw string) (GuestSession, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return GuestSession{}, ErrGuestTokenInvalid
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return GuestSession{}, ErrGuestTokenInvalid
	}
	var hdr guestTokenHeader
	if err := json.Unmarshal(headerJSON, &hdr); err != nil || hdr.Typ != guestTokenTyp {
		return GuestSession{}, ErrGuestTokenInvalid
	}

	if !s.validSignature(parts[0]+"."+parts[1], parts[2]) {
		return GuestSession{}, ErrGuestTokenInvalid
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return GuestSession{}, ErrGuestTokenInvalid
	}
	var claims guestTokenClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return GuestSession{}, ErrGuestTokenInvalid
	}

	if claims.Iss != guestTokenIssuer || claims.Aud != guestTokenAudience {
		return GuestSession{}, ErrGuestTokenInvalid
	}
	if time.Now().Unix() > claims.Exp {
		return GuestSession{}, ErrGuestTokenExpired
	}

	return claimsToGuestSession(claims)
}

// IsGuestToken reports whether the raw token's Typ header is GUEST.
// Like IsContextToken it only reads the header: routing, not verification.
func IsGuestToken(raw string) bool {
	parts := strings.SplitN(raw, ".", 2)
	if len(parts) < 1 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var hdr guestTokenHeader
	if err := json.Unmarshal(b, &hdr); err != nil {
		return false
	}
	return hdr.Typ == guestTokenTyp
}

func (s *GuestTokenSigner) sign(claims guestTokenClaims) (string, error) {
	hdrJSON, err := json.Marshal(guestTokenHeader{Alg: "HS256", Typ: guestTokenTyp})
	if err != nil {
		return "", fmt.Errorf("auth: marshal guest token header: %w", err)
	}
	payJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("auth: marshal guest token claims: %w", err)
	}

	hdrEnc := base64.RawURLEncoding.EncodeToString(hdrJSON)
	payEnc := base64.RawURLEncoding.EncodeToString(payJSON)
	sigEnc := s.computeSignature(hdrEnc + "." + payEnc)

	return hdrEnc + "." + payEnc + "." + sigEnc, nil
}

func (s *GuestTokenSigner) computeSignature(input string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(input))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *GuestTokenSigner) validSignature(input, gotEnc string) bool {
	want := s.computeSignature(input)
	return hmac.Equal([]byte(want), []byte(gotEnc))
}

func claimsToGuestSession(c guestTokenClaims) (GuestSession, error) {
	gs := GuestSession{ExpiresAt: time.Unix(c.Exp, 0)}

	var err error
	if gs.TenantID, err = uuid.Parse(c.TenantID); err != nil {
		return GuestSession{}, fmt.Errorf("auth: invalid tid in guest token: %w", err)
	}
	if gs.BranchID, err = uuid.Parse(c.BranchID); err != nil {
		return GuestSession{}, fmt.Errorf("auth: invalid bid in guest token: %w", err)
	}
	if gs.TableID, err = uuid.Parse(c.TableID); err != nil {
		return GuestSession{}, fmt.Errorf("auth: invalid table_id in guest token: %w", err)
	}
	if gs.QRCodeID, err = uuid.Parse(c.QRCodeID); err != nil {
		return GuestSession{}, fmt.Errorf("auth: invalid qr in guest token: %w", err)
	}
	if gs.SessionID, err = uuid.Parse(c.SessionID); err != nil {
		return GuestSession{}, fmt.Errorf("auth: invalid sid in guest token: %w", err)
	}
	return gs, nil
}

// guestContextKey is a distinct type from principalKey's contextKey on
// purpose: two `struct{}` keys of the SAME type compare equal, so reusing
// contextKey{} here would make WithGuestSession silently overwrite the
// request's Principal (and vice versa).
type guestContextKey struct{}

var guestSessionKey = guestContextKey{}

// WithGuestSession stores the given GuestSession in the context.
func WithGuestSession(ctx context.Context, g GuestSession) context.Context {
	return context.WithValue(ctx, guestSessionKey, g)
}

// GuestFromContext returns the GuestSession stored in ctx, if any.
func GuestFromContext(ctx context.Context) (GuestSession, bool) {
	g, ok := ctx.Value(guestSessionKey).(GuestSession)
	return g, ok
}
