package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/service"
)

// TestHandler_Error_MapsSentinels verifies h.error translates the module's
// sentinel errors to the expected HTTP status codes via errors.Is, including
// through fmt.Errorf %w wrapping (the shape wrapErr and Close/service methods
// actually return).
func TestHandler_Error_MapsSentinels(t *testing.T) {
	h := &Handler{logger: zap.NewNop()}

	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{
			name:     "not found",
			err:      fmt.Errorf("pos/service/check: get by id: %w", pub.ErrNotFound),
			wantCode: http.StatusNotFound,
		},
		{
			name:     "invalid transition",
			err:      fmt.Errorf("pos/service/check: close: %w", pub.ErrInvalidTransition),
			wantCode: http.StatusConflict,
		},
		{
			name:     "branch forbidden",
			err:      fmt.Errorf("pos/service/check: close: %w", pub.ErrBranchForbidden),
			wantCode: http.StatusForbidden,
		},
		{
			// This is the regression case: CheckService.Close returns
			// service.ErrInsufficientPayment directly (not through wrapErr),
			// and before this fix h.error had no branch for it, so it fell
			// through to a generic 500 instead of a distinguishable 4xx.
			name:     "insufficient payment",
			err:      fmt.Errorf("pos/service/check: close: %w", service.ErrInsufficientPayment),
			wantCode: http.StatusConflict,
		},
		{
			name:     "unmapped error",
			err:      fmt.Errorf("pos/service/check: some unexpected failure"),
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/probe", nil)
			h.error(rec, req, tt.err)
			assert.Equal(t, tt.wantCode, rec.Code)
		})
	}
}
