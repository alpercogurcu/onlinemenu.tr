package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestIssueStaffForSession_VerifyRoundTrip(t *testing.T) {
	s := newTestSigner(t)
	personID, tenantID, branchID, sessionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	roleIDs := []uuid.UUID{uuid.New()}

	token, err := s.IssueStaffForSession(personID, tenantID, branchID, sessionID, roleIDs, nil)
	require.NoError(t, err)

	p, err := s.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, personID, p.PersonID)
	assert.Equal(t, tenantID, p.TenantID)
	assert.Equal(t, branchID, p.BranchID)
	assert.Equal(t, sessionID, p.SessionID)
	assert.True(t, p.IsStaff())
}

func TestIssueStaffForSession_DeadlineClampsExpiry(t *testing.T) {
	s := newTestSigner(t)
	deadline := time.Now().Add(30 * time.Minute)

	token, err := s.IssueStaffForSession(uuid.New(), uuid.New(), uuid.New(), uuid.New(), nil, &deadline)
	require.NoError(t, err)

	claims := decodeClaims(t, token)
	assert.InDelta(t, deadline.Unix(), claims.Exp, 2, "exp must clamp to the earlier deadline, not the full 8h TTL")
}

func TestIssueStaff_LegacyToken_SessionIDIsNil(t *testing.T) {
	// A token minted by the pre-existing IssueStaff path (the normal
	// Keycloak /auth/context flow) carries no sid claim at all. Verify must
	// decode that as uuid.Nil, not fail to parse — this is what lets
	// RequireOpenSession's short-circuit be free for every such token.
	s := newTestSigner(t)
	token, err := s.IssueStaff(uuid.New(), uuid.New(), uuid.New(), nil)
	require.NoError(t, err)

	p, err := s.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, p.SessionID)
}

// decodeClaims is a tiny local helper to inspect the exp claim without
// duplicating context_token.go's private sign/verify machinery.
func decodeClaims(t *testing.T, token string) contextTokenClaims {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims contextTokenClaims
	require.NoError(t, json.Unmarshal(raw, &claims))
	return claims
}

type fakeSessionValidator struct {
	open bool
	err  error
}

func (f fakeSessionValidator) IsOpen(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return f.open, f.err
}

func TestRequireOpenSession_SkipsWhenNoSessionID(t *testing.T) {
	// nil validator would fail closed if ever consulted — this test proves
	// it is never consulted for a principal with no SessionID.
	mw := RequireOpenSession(nil, zap.NewNop())
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{
		PersonID: uuid.New(), Ctx: ContextStaff, TenantID: uuid.New(), BranchID: uuid.New(),
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireOpenSession_NilValidator_FailsClosed(t *testing.T) {
	mw := RequireOpenSession(nil, zap.NewNop())
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{
		PersonID: uuid.New(), Ctx: ContextStaff, TenantID: uuid.New(), BranchID: uuid.New(),
		SessionID: uuid.New(),
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.False(t, called, "handler must not run when a session-scoped token has no validator to check it against")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireOpenSession_OpenSession_Allows(t *testing.T) {
	mw := RequireOpenSession(fakeSessionValidator{open: true}, zap.NewNop())
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{
		PersonID: uuid.New(), Ctx: ContextStaff, TenantID: uuid.New(), BranchID: uuid.New(),
		SessionID: uuid.New(),
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireOpenSession_ClosedSession_Rejects(t *testing.T) {
	mw := RequireOpenSession(fakeSessionValidator{open: false}, zap.NewNop())
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{
		PersonID: uuid.New(), Ctx: ContextStaff, TenantID: uuid.New(), BranchID: uuid.New(),
		SessionID: uuid.New(),
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.False(t, called, "handler must not run once the session has closed")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireOpenSession_ValidatorError_FailsClosed(t *testing.T) {
	mw := RequireOpenSession(fakeSessionValidator{err: errors.New("db down")}, zap.NewNop())
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{
		PersonID: uuid.New(), Ctx: ContextStaff, TenantID: uuid.New(), BranchID: uuid.New(),
		SessionID: uuid.New(),
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
