package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/inventory/domain"
	pub "onlinemenu.tr/internal/modules/inventory/public"
	"onlinemenu.tr/internal/modules/inventory/service"
)

// validReq returns a baseline valid RecordMovementRequest, overridden per case.
func validReq() service.RecordMovementRequest {
	return service.RecordMovementRequest{
		WarehouseID: uuid.New(),
		StockItemID: uuid.New(),
		Type:        domain.MovementTypeIn,
		Quantity:    10,
		Unit:        "kg",
	}
}

func TestValidateMovement_In(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(r *service.RecordMovementRequest)
		wantErr   bool
		errSubstr string
	}{
		{
			name:   "positive quantity valid",
			mutate: func(r *service.RecordMovementRequest) { r.Quantity = 10 },
		},
		{
			name:      "zero quantity invalid",
			mutate:    func(r *service.RecordMovementRequest) { r.Quantity = 0 },
			wantErr:   true,
			errSubstr: "must not be zero",
		},
		{
			name:      "negative quantity invalid for in",
			mutate:    func(r *service.RecordMovementRequest) { r.Quantity = -5 },
			wantErr:   true,
			errSubstr: "positive quantity magnitude",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validReq()
			tc.mutate(&req)
			err := service.ValidateMovementForTest(req)
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

func TestValidateMovement_OutReserveRelease(t *testing.T) {
	for _, mt := range []domain.MovementType{
		domain.MovementTypeOut,
		domain.MovementTypeReserve,
		domain.MovementTypeRelease,
		domain.MovementTypeTransfer,
	} {
		t.Run(string(mt), func(t *testing.T) {
			t.Run("positive magnitude valid", func(t *testing.T) {
				req := validReq()
				req.Type = mt
				req.Quantity = 3
				assert.NoError(t, service.ValidateMovementForTest(req))
			})
			t.Run("negative magnitude invalid", func(t *testing.T) {
				req := validReq()
				req.Type = mt
				req.Quantity = -3
				err := service.ValidateMovementForTest(req)
				require.Error(t, err)
				var vErr *pub.ValidationError
				require.ErrorAs(t, err, &vErr)
				assert.Contains(t, vErr.Error(), "positive quantity magnitude")
			})
			t.Run("zero magnitude invalid", func(t *testing.T) {
				req := validReq()
				req.Type = mt
				req.Quantity = 0
				require.Error(t, service.ValidateMovementForTest(req))
			})
		})
	}
}

func TestValidateMovement_Adjust(t *testing.T) {
	t.Run("positive delta valid", func(t *testing.T) {
		req := validReq()
		req.Type = domain.MovementTypeAdjust
		req.Quantity = 1
		assert.NoError(t, service.ValidateMovementForTest(req))
	})
	t.Run("negative delta valid", func(t *testing.T) {
		req := validReq()
		req.Type = domain.MovementTypeAdjust
		req.Quantity = -1
		assert.NoError(t, service.ValidateMovementForTest(req))
	})
	t.Run("zero delta invalid", func(t *testing.T) {
		req := validReq()
		req.Type = domain.MovementTypeAdjust
		req.Quantity = 0
		require.Error(t, service.ValidateMovementForTest(req))
	})
}

func TestValidateMovement_InvalidType(t *testing.T) {
	req := validReq()
	req.Type = "unknown"
	err := service.ValidateMovementForTest(req)
	require.Error(t, err)
	var vErr *pub.ValidationError
	require.ErrorAs(t, err, &vErr)
	assert.Contains(t, vErr.Error(), "invalid movement type")
}

func TestValidateMovement_MissingIdentifiers(t *testing.T) {
	t.Run("missing warehouse_id", func(t *testing.T) {
		req := validReq()
		req.WarehouseID = uuid.Nil
		err := service.ValidateMovementForTest(req)
		require.Error(t, err)
		var vErr *pub.ValidationError
		require.ErrorAs(t, err, &vErr)
		assert.Contains(t, vErr.Error(), "warehouse_id")
	})
	t.Run("missing stock_item_id", func(t *testing.T) {
		req := validReq()
		req.StockItemID = uuid.Nil
		err := service.ValidateMovementForTest(req)
		require.Error(t, err)
		var vErr *pub.ValidationError
		require.ErrorAs(t, err, &vErr)
		assert.Contains(t, vErr.Error(), "stock_item_id")
	})
}

func TestSignedDelta(t *testing.T) {
	cases := []struct {
		typ      domain.MovementType
		quantity float64
		want     float64
	}{
		{domain.MovementTypeIn, 10, 10},
		{domain.MovementTypeRelease, 10, 10},
		{domain.MovementTypeOut, 10, -10},
		{domain.MovementTypeReserve, 10, -10},
		{domain.MovementTypeTransfer, 10, -10},
		{domain.MovementTypeAdjust, -4, -4},
		{domain.MovementTypeAdjust, 4, 4},
	}
	for _, tc := range cases {
		t.Run(string(tc.typ), func(t *testing.T) {
			assert.Equal(t, tc.want, service.SignedDeltaForTest(tc.typ, tc.quantity))
		})
	}
}
