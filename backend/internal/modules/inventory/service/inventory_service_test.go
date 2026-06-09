package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/inventory/domain"
	pub "onlinemenu.tr/internal/modules/inventory/public"
	"onlinemenu.tr/internal/modules/inventory/service"
)

// validateAdjustmentFixture exercises the exported service via RecordAdjustmentRequest
// without touching the database by checking validation errors only.
// The unexported validateAdjustment function is tested through RecordAdjustment's
// early-return path: all inputs are validated before any DB call is attempted.

func TestValidateAdjustment_Restock(t *testing.T) {
	cases := []struct {
		name      string
		req       service.RecordAdjustmentRequest
		wantErr   bool
		errSubstr string
	}{
		{
			name: "restock positive delta valid",
			req: service.RecordAdjustmentRequest{
				Type: domain.TransactionTypeRestock, QuantityDelta: 10,
			},
		},
		{
			name: "restock zero delta invalid",
			req: service.RecordAdjustmentRequest{
				Type: domain.TransactionTypeRestock, QuantityDelta: 0,
			},
			wantErr: true, errSubstr: "must not be zero",
		},
		{
			name: "restock negative delta invalid",
			req: service.RecordAdjustmentRequest{
				Type: domain.TransactionTypeRestock, QuantityDelta: -5,
			},
			wantErr: true, errSubstr: "positive",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := invokeValidation(tc.req)
			if tc.wantErr {
				require.Error(t, err)
				var vErr *pub.ValidationError
				require.ErrorAs(t, err, &vErr)
				assert.Contains(t, vErr.Error(), tc.errSubstr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateAdjustment_ConsumptionAndWaste(t *testing.T) {
	for _, txType := range []domain.TransactionType{
		domain.TransactionTypeConsumption,
		domain.TransactionTypeWaste,
	} {
		t.Run(string(txType), func(t *testing.T) {
			t.Run("negative delta valid", func(t *testing.T) {
				err := invokeValidation(service.RecordAdjustmentRequest{
					Type: txType, QuantityDelta: -3,
				})
				assert.NoError(t, err)
			})
			t.Run("positive delta invalid", func(t *testing.T) {
				err := invokeValidation(service.RecordAdjustmentRequest{
					Type: txType, QuantityDelta: +3,
				})
				require.Error(t, err)
				var vErr *pub.ValidationError
				require.ErrorAs(t, err, &vErr)
				assert.Contains(t, vErr.Error(), "negative")
			})
			t.Run("zero delta invalid", func(t *testing.T) {
				err := invokeValidation(service.RecordAdjustmentRequest{
					Type: txType, QuantityDelta: 0,
				})
				require.Error(t, err)
			})
		})
	}
}

func TestValidateAdjustment_Adjustment(t *testing.T) {
	t.Run("positive delta valid", func(t *testing.T) {
		assert.NoError(t, invokeValidation(service.RecordAdjustmentRequest{
			Type: domain.TransactionTypeAdjustment, QuantityDelta: +1,
		}))
	})
	t.Run("negative delta valid", func(t *testing.T) {
		assert.NoError(t, invokeValidation(service.RecordAdjustmentRequest{
			Type: domain.TransactionTypeAdjustment, QuantityDelta: -1,
		}))
	})
	t.Run("zero delta invalid", func(t *testing.T) {
		require.Error(t, invokeValidation(service.RecordAdjustmentRequest{
			Type: domain.TransactionTypeAdjustment, QuantityDelta: 0,
		}))
	})
}

func TestValidateAdjustment_InvalidType(t *testing.T) {
	err := invokeValidation(service.RecordAdjustmentRequest{
		Type: "unknown", QuantityDelta: 5,
	})
	require.Error(t, err)
	var vErr *pub.ValidationError
	require.ErrorAs(t, err, &vErr)
	assert.Contains(t, vErr.Error(), "invalid transaction type")
}

// invokeValidation calls service.ValidateAdjustment which is the exported
// shim used only in tests to exercise the validation path without a DB.
func invokeValidation(req service.RecordAdjustmentRequest) error {
	return service.ValidateAdjustmentForTest(req)
}
