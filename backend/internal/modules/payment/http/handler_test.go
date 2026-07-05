package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/platform/auth"
)

// TestListPayments_InvalidCheckID_Returns400 verifies the check_id query
// parameter is validated as a UUID before the handler ever reaches
// PaymentService.ListByCheck, and rejects it with 400 rather than letting an
// unparsable value reach the DB layer as a query argument.
func TestListPayments_InvalidCheckID_Returns400(t *testing.T) {
	h := &Handler{logger: zap.NewNop()}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments?check_id=not-a-uuid", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		PersonID: uuid.New(),
	}))
	rec := httptest.NewRecorder()

	h.listPayments(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestListPayments_Unauthenticated_Returns401(t *testing.T) {
	h := &Handler{logger: zap.NewNop()}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments", nil)
	rec := httptest.NewRecorder()

	h.listPayments(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
