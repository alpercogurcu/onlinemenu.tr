package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testGuestSecret = []byte("this-is-a-32-byte-guest-secret!!")

func newTestGuestSigner(t *testing.T) *GuestTokenSigner {
	t.Helper()
	s, err := NewGuestTokenSigner(testGuestSecret)
	require.NoError(t, err)
	return s
}

func newTestGuestToken(t *testing.T, s *GuestTokenSigner) (string, GuestSession) {
	t.Helper()
	want := GuestSession{
		TenantID:  uuid.New(),
		BranchID:  uuid.New(),
		TableID:   uuid.New(),
		QRCodeID:  uuid.New(),
		SessionID: uuid.New(),
	}
	token, err := s.IssueGuest(want.TenantID, want.BranchID, want.TableID, want.QRCodeID, want.SessionID)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	return token, want
}

func TestNewGuestTokenSigner_RejectsShortSecret(t *testing.T) {
	_, err := NewGuestTokenSigner([]byte("short"))
	require.Error(t, err)
}

func TestIssueGuest_VerifyRoundTrip(t *testing.T) {
	s := newTestGuestSigner(t)
	token, want := newTestGuestToken(t, s)

	got, err := s.VerifyGuest(token)
	require.NoError(t, err)

	assert.Equal(t, want.TenantID, got.TenantID)
	assert.Equal(t, want.BranchID, got.BranchID)
	assert.Equal(t, want.TableID, got.TableID)
	assert.Equal(t, want.QRCodeID, got.QRCodeID)
	assert.Equal(t, want.SessionID, got.SessionID)
	assert.WithinDuration(t, time.Now().Add(guestTokenTTL), got.ExpiresAt, time.Minute)
}

// TestGuestToken_TypHeaderIsGUEST is the structural guarantee behind "a guest
// token is worthless on a staff endpoint": because typ is not CTX,
// IsContextToken returns false and Middleware sends the token down the
// Keycloak path, where it cannot possibly verify.
func TestGuestToken_TypHeaderIsGUEST(t *testing.T) {
	s := newTestGuestSigner(t)
	token, _ := newTestGuestToken(t, s)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)

	var hdr struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	require.NoError(t, json.Unmarshal(raw, &hdr))
	assert.Equal(t, "GUEST", hdr.Typ)
	assert.Equal(t, "HS256", hdr.Alg)

	assert.False(t, IsContextToken(token), "a guest token must never be routed as a context token")
	assert.True(t, IsGuestToken(token))
}

// TestGuestToken_IsRejectedByContextSigner and its inverse below are the two
// halves of the staff/guest separation: neither signer may accept the other's
// token, even though both are HMAC-SHA256 JWT-shaped strings.
func TestGuestToken_IsRejectedByContextSigner(t *testing.T) {
	guestSigner := newTestGuestSigner(t)
	token, _ := newTestGuestToken(t, guestSigner)

	ctxSigner, err := NewContextTokenSigner(testGuestSecret)
	require.NoError(t, err)

	// Same secret on purpose: rejection must come from the typ check, not
	// from a signature mismatch that a shared-secret deployment would lose.
	_, err = ctxSigner.Verify(token)
	require.ErrorIs(t, err, ErrTokenInvalid)
}

func TestVerifyGuest_RejectsStaffContextToken(t *testing.T) {
	ctxSigner, err := NewContextTokenSigner(testGuestSecret)
	require.NoError(t, err)
	staffToken, err := ctxSigner.IssueStaff(uuid.New(), uuid.New(), uuid.New(), []uuid.UUID{uuid.New()})
	require.NoError(t, err)

	guestSigner := newTestGuestSigner(t)
	_, err = guestSigner.VerifyGuest(staffToken)
	require.ErrorIs(t, err, ErrGuestTokenInvalid)

	assert.False(t, IsGuestToken(staffToken))
	assert.True(t, IsContextToken(staffToken))
}

func TestVerifyGuest_RejectsWrongSecret(t *testing.T) {
	token, _ := newTestGuestToken(t, newTestGuestSigner(t))

	other, err := NewGuestTokenSigner([]byte("a-completely-different-32-byte!!!"))
	require.NoError(t, err)

	_, err = other.VerifyGuest(token)
	require.ErrorIs(t, err, ErrGuestTokenInvalid)
}

func TestVerifyGuest_RejectsExpired(t *testing.T) {
	s := newTestGuestSigner(t)
	token, err := s.sign(guestTokenClaims{
		Iss:       guestTokenIssuer,
		Aud:       guestTokenAudience,
		TenantID:  uuid.New().String(),
		BranchID:  uuid.New().String(),
		TableID:   uuid.New().String(),
		QRCodeID:  uuid.New().String(),
		SessionID: uuid.New().String(),
		Exp:       time.Now().Add(-time.Minute).Unix(),
	})
	require.NoError(t, err)

	_, err = s.VerifyGuest(token)
	require.ErrorIs(t, err, ErrGuestTokenExpired)
}

func TestVerifyGuest_RejectsWrongAudience(t *testing.T) {
	s := newTestGuestSigner(t)
	token, err := s.sign(guestTokenClaims{
		Iss:       guestTokenIssuer,
		Aud:       "onlinemenu-backend",
		TenantID:  uuid.New().String(),
		BranchID:  uuid.New().String(),
		TableID:   uuid.New().String(),
		QRCodeID:  uuid.New().String(),
		SessionID: uuid.New().String(),
		Exp:       time.Now().Add(guestTokenTTL).Unix(),
	})
	require.NoError(t, err)

	// Correctly signed, correctly typed, in-date — rejected purely on aud,
	// which is what stops a future token minted by this signer for another
	// audience from being replayed at the storefront.
	_, err = s.VerifyGuest(token)
	require.ErrorIs(t, err, ErrGuestTokenInvalid)
}

func TestVerifyGuest_RejectsWrongIssuer(t *testing.T) {
	s := newTestGuestSigner(t)
	token, err := s.sign(guestTokenClaims{
		Iss:       "someone-else",
		Aud:       guestTokenAudience,
		TenantID:  uuid.New().String(),
		BranchID:  uuid.New().String(),
		TableID:   uuid.New().String(),
		QRCodeID:  uuid.New().String(),
		SessionID: uuid.New().String(),
		Exp:       time.Now().Add(guestTokenTTL).Unix(),
	})
	require.NoError(t, err)

	_, err = s.VerifyGuest(token)
	require.ErrorIs(t, err, ErrGuestTokenInvalid)
}

func TestVerifyGuest_RejectsMalformed(t *testing.T) {
	s := newTestGuestSigner(t)
	valid, _ := newTestGuestToken(t, s)
	parts := strings.Split(valid, ".")

	tests := []struct {
		name  string
		token string
	}{
		{name: "empty", token: ""},
		{name: "two segments", token: parts[0] + "." + parts[1]},
		{name: "four segments", token: valid + ".extra"},
		{name: "garbage header", token: "!!!." + parts[1] + "." + parts[2]},
		{name: "tampered payload", token: parts[0] + "." + parts[1] + "x." + parts[2]},
		{name: "tampered signature", token: parts[0] + "." + parts[1] + "." + parts[2] + "x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.VerifyGuest(tt.token)
			require.ErrorIs(t, err, ErrGuestTokenInvalid)
		})
	}
}

func TestVerifyGuest_RejectsNonUUIDClaims(t *testing.T) {
	s := newTestGuestSigner(t)
	token, err := s.sign(guestTokenClaims{
		Iss:       guestTokenIssuer,
		Aud:       guestTokenAudience,
		TenantID:  "not-a-uuid",
		BranchID:  uuid.New().String(),
		TableID:   uuid.New().String(),
		QRCodeID:  uuid.New().String(),
		SessionID: uuid.New().String(),
		Exp:       time.Now().Add(guestTokenTTL).Unix(),
	})
	require.NoError(t, err)

	_, err = s.VerifyGuest(token)
	require.Error(t, err)
}

// TestGuestSessionContextKey_DoesNotCollideWithPrincipal guards a failure mode
// that is silent by construction: two context keys declared as the zero value
// of the SAME struct{} type compare equal, so a guest session stored under a
// shared key would overwrite the request's Principal (and be readable as one).
func TestGuestSessionContextKey_DoesNotCollideWithPrincipal(t *testing.T) {
	ctx := context.Background()
	p := Principal{PersonID: uuid.New(), Ctx: ContextStaff, TenantID: uuid.New(), BranchID: uuid.New()}
	g := GuestSession{TenantID: uuid.New(), BranchID: uuid.New(), SessionID: uuid.New()}

	ctx = WithPrincipal(ctx, p)
	ctx = WithGuestSession(ctx, g)

	gotP, err := FromContext(ctx)
	require.NoError(t, err, "principal must survive a guest session being added")
	assert.Equal(t, p.PersonID, gotP.PersonID)

	gotG, ok := GuestFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, g.SessionID, gotG.SessionID)
	assert.NotEqual(t, gotP.TenantID, gotG.TenantID, "the two values must be independently stored")
}

func TestGuestFromContext_AbsentIsNotOK(t *testing.T) {
	_, ok := GuestFromContext(context.Background())
	assert.False(t, ok)

	// A staff principal in context must never be readable as a guest session.
	ctx := WithPrincipal(context.Background(), Principal{PersonID: uuid.New(), Ctx: ContextStaff})
	_, ok = GuestFromContext(ctx)
	assert.False(t, ok)
}
