package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/service"
	"onlinemenu.tr/internal/platform/auth"
)

// fakeCashSessionPinService is a minimal stand-in for
// *service.CashSessionPinService satisfying cashSessionPinService — only the
// method(s) a given test cares about need to return something meaningful,
// the rest are never called.
type fakeCashSessionPinService struct {
	joinErr error
}

func (f *fakeCashSessionPinService) Join(context.Context, auth.Principal, uuid.UUID, string) error {
	return f.joinErr
}

func (f *fakeCashSessionPinService) Switch(context.Context, auth.Principal, uuid.UUID, uuid.UUID, string) (string, error) {
	return "", nil
}

func (f *fakeCashSessionPinService) ListParticipants(context.Context, auth.Principal, uuid.UUID) ([]service.CashSessionParticipantView, error) {
	return nil, nil
}

func (f *fakeCashSessionPinService) ResetPin(context.Context, auth.Principal, uuid.UUID, uuid.UUID) error {
	return nil
}

// joinRequest builds an authenticated POST .../participants request carrying
// sessionID as the chi {id} route param.
func joinRequest(sessionID uuid.UUID) *http.Request {
	r := httptest.NewRequest(http.MethodPost,
		"/api/v1/payments/cash-sessions/"+sessionID.String()+"/participants",
		strings.NewReader(`{}`))
	ctx := auth.WithPrincipal(r.Context(), auth.Principal{
		Ctx:      auth.ContextStaff,
		TenantID: testTenantID,
		BranchID: testBranchID,
		PersonID: uuid.New(),
	})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", sessionID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	return r.WithContext(ctx)
}

// TestJoinCashSession_ErrorCodes pins the 403-body split Join must produce:
// ADR-DATA-008's two distinct "forbidden" outcomes (a pin-switched principal
// vs. a wrong-branch principal) used to collapse into the same bare 403
// "forbidden" text, giving the POS client nothing to act on. Each must now
// carry a distinguishable `code` so the client can react (e.g. tell a
// session-scoped principal to re-authenticate via Keycloak) instead of
// showing one generic message for both.
func TestJoinCashSession_ErrorCodes(t *testing.T) {
	tests := []struct {
		name       string
		joinErr    error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "session-scoped principal",
			joinErr:    pub.ErrSessionScopedPrincipal,
			wantStatus: http.StatusForbidden,
			wantCode:   codeSessionScopedPrincipal,
		},
		{
			name:       "branch forbidden",
			joinErr:    pub.ErrBranchForbidden,
			wantStatus: http.StatusForbidden,
			wantCode:   codeBranchForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{
				sessionPin: &fakeCashSessionPinService{joinErr: tt.joinErr},
				logger:     zap.NewNop(),
			}
			req := joinRequest(uuid.New())
			rec := httptest.NewRecorder()

			h.joinCashSession(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			var body map[string]string
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, tt.wantCode, body["code"])
			assert.Equal(t, "forbidden", body["error"])
		})
	}
}

// TestJoinCashSession_Success guards that the two new coded branches did not
// disturb the happy path: no service error still answers 204 with no body.
func TestJoinCashSession_Success(t *testing.T) {
	h := &Handler{
		sessionPin: &fakeCashSessionPinService{joinErr: nil},
		logger:     zap.NewNop(),
	}
	req := joinRequest(uuid.New())
	rec := httptest.NewRecorder()

	h.joinCashSession(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.Bytes())
}
