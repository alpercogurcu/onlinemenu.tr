package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	pub "onlinemenu.tr/internal/modules/payment/public"
)

// TestRegisterSaleError_MapsSentinels covers POST /api/v1/payments's error
// contract, including the 2026-09-15 regression: a sale against a closed or
// cancelled adisyon must be a 409 carrying a machine-readable code, not the
// 201 production actually returned.
func TestRegisterSaleError_MapsSentinels(t *testing.T) {
	h := &Handler{logger: zap.NewNop()}

	tests := []struct {
		name         string
		err          error
		wantStatus   int
		wantBodyCode string
	}{
		{
			name:         "check not open",
			err:          fmt.Errorf("payment/service: register sale: %w", pub.ErrCheckNotOpen),
			wantStatus:   http.StatusConflict,
			wantBodyCode: codeCheckNotOpen,
		},
		{
			name:         "check branch mismatch",
			err:          fmt.Errorf("payment/service: register sale: %w", pub.ErrCheckBranchMismatch),
			wantStatus:   http.StatusConflict,
			wantBodyCode: codeCheckBranchMismatch,
		},
		{
			name:         "check not found",
			err:          fmt.Errorf("payment/service: register sale: %w", pub.ErrCheckNotFound),
			wantStatus:   http.StatusUnprocessableEntity,
			wantBodyCode: codeCheckNotFound,
		},
		{
			name:       "no cash session open",
			err:        fmt.Errorf("payment/service: register sale: %w", pub.ErrNoCashSessionOpen),
			wantStatus: http.StatusConflict,
		},
		{
			name:       "invalid input",
			err:        fmt.Errorf("%w: amount_total must be positive", pub.ErrInvalidInput),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "unmapped error",
			err:        errors.New("payment/service: register sale: conn closed"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.registerSaleError(rec, tt.err)
			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantBodyCode == "" {
				return
			}
			var body errorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, tt.wantBodyCode, body.Code)
			assert.NotEmpty(t, body.Error)
		})
	}
}
