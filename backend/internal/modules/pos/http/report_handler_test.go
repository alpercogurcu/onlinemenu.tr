package http

// Table-driven unit tests for saleDetails' input-validation branches — pure
// httptest, no service/DB involved (h.reports is left nil; a test that
// somehow reached it would panic, which is itself evidence the branch under
// test failed to short-circuit before calling the service, matching
// order_ids_test.go's "servise çağrılmadan dönmeli" contract for its own
// batch-read validation).

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/platform/auth"
)

func newSaleDetailsRequest(query string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/reports/sale-details?"+query, nil)
	return req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
	}))
}

func TestSaleDetails_ValidationErrors(t *testing.T) {
	h := &Handler{logger: zap.NewNop()}
	branchID := uuid.New().String()

	tests := []struct {
		name    string
		query   string
		wantMsg string
	}{
		{
			name:    "missing branch_id",
			query:   "from=2026-09-04T00:00:00Z&to=2026-09-05T00:00:00Z&tz=Europe/Istanbul",
			wantMsg: "invalid branch_id",
		},
		{
			name:    "malformed branch_id",
			query:   "branch_id=not-a-uuid&from=2026-09-04T00:00:00Z&to=2026-09-05T00:00:00Z&tz=Europe/Istanbul",
			wantMsg: "invalid branch_id",
		},
		{
			name:    "missing from",
			query:   "branch_id=" + branchID + "&to=2026-09-05T00:00:00Z&tz=Europe/Istanbul",
			wantMsg: "from and to are required (RFC3339)",
		},
		{
			name:    "missing to",
			query:   "branch_id=" + branchID + "&from=2026-09-04T00:00:00Z&tz=Europe/Istanbul",
			wantMsg: "from and to are required (RFC3339)",
		},
		{
			name:    "malformed from",
			query:   "branch_id=" + branchID + "&from=not-a-time&to=2026-09-05T00:00:00Z&tz=Europe/Istanbul",
			wantMsg: "from and to are required (RFC3339)",
		},
		{
			name:    "malformed to",
			query:   "branch_id=" + branchID + "&from=2026-09-04T00:00:00Z&to=not-a-time&tz=Europe/Istanbul",
			wantMsg: "from and to are required (RFC3339)",
		},
		{
			name:    "non-RFC3339 date-only from/to",
			query:   "branch_id=" + branchID + "&from=2026-09-04&to=2026-09-05&tz=Europe/Istanbul",
			wantMsg: "from and to are required (RFC3339)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.saleDetails(rec, newSaleDetailsRequest(tt.query))

			require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
			assert.Equal(t, tt.wantMsg+"\n", rec.Body.String())
		})
	}
}

// TestSaleDetails_Unauthorized pins the shared requirePrincipal guard (same
// as every other handler in this package): no principal in context -> 401,
// before any query parsing happens.
func TestSaleDetails_Unauthorized(t *testing.T) {
	h := &Handler{logger: zap.NewNop()}
	req := httptest.NewRequest(http.MethodGet, "/reports/sale-details", nil)
	rec := httptest.NewRecorder()

	h.saleDetails(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
