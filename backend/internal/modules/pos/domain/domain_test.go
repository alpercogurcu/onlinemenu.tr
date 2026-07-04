package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"onlinemenu.tr/internal/modules/pos/domain"
)

func TestCheckStatus_Valid(t *testing.T) {
	valid := []domain.CheckStatus{
		domain.CheckStatusOpen,
		domain.CheckStatusClosed,
		domain.CheckStatusCancelled,
	}
	for _, s := range valid {
		assert.True(t, s.Valid(), "expected %q to be valid", s)
	}
	assert.False(t, domain.CheckStatus("unknown").Valid())
}

func TestOrderChannel_Valid(t *testing.T) {
	valid := []domain.OrderChannel{
		domain.OrderChannelDineIn,
		domain.OrderChannelTakeaway,
		domain.OrderChannelDelivery,
	}
	for _, c := range valid {
		assert.True(t, c.Valid(), "expected %q to be valid", c)
	}
	assert.False(t, domain.OrderChannel("drive_through").Valid())
}

func TestOrderStatus_Valid(t *testing.T) {
	valid := []domain.OrderStatus{
		domain.OrderStatusPending,
		domain.OrderStatusAccepted,
		domain.OrderStatusRejected,
		domain.OrderStatusPreparing,
		domain.OrderStatusReady,
		domain.OrderStatusDelivered,
		domain.OrderStatusCancelled,
	}
	for _, s := range valid {
		assert.True(t, s.Valid(), "expected %q to be valid", s)
	}
	assert.False(t, domain.OrderStatus("shipped").Valid())
}

func TestTransitionOrderStatus(t *testing.T) {
	allStatuses := []domain.OrderStatus{
		domain.OrderStatusPending,
		domain.OrderStatusAccepted,
		domain.OrderStatusRejected,
		domain.OrderStatusPreparing,
		domain.OrderStatusReady,
		domain.OrderStatusDelivered,
		domain.OrderStatusCancelled,
	}

	allowed := map[domain.OrderStatus]map[domain.OrderStatus]bool{
		domain.OrderStatusPending: {
			domain.OrderStatusAccepted:  true,
			domain.OrderStatusRejected:  true,
			domain.OrderStatusCancelled: true,
		},
		domain.OrderStatusAccepted: {
			domain.OrderStatusPreparing: true,
			domain.OrderStatusCancelled: true,
		},
		domain.OrderStatusPreparing: {
			domain.OrderStatusReady:     true,
			domain.OrderStatusCancelled: true,
		},
		domain.OrderStatusReady: {
			domain.OrderStatusDelivered: true,
			domain.OrderStatusCancelled: true,
		},
		// rejected, delivered, cancelled are terminal: no entries.
	}

	for _, from := range allStatuses {
		for _, to := range allStatuses {
			from, to := from, to
			wantOK := allowed[from][to]
			t.Run(string(from)+"->"+string(to), func(t *testing.T) {
				err := domain.TransitionOrderStatus(from, to)
				if wantOK {
					assert.NoError(t, err)
				} else {
					assert.Error(t, err)
					assert.True(t, errors.Is(err, domain.ErrInvalidTransition))
				}
			})
		}
	}
}

func TestTransitionOrderStatus_RejectsUnknownTarget(t *testing.T) {
	err := domain.TransitionOrderStatus(domain.OrderStatusPending, domain.OrderStatus("shipped"))
	assert.ErrorIs(t, err, domain.ErrInvalidTransition)
}
