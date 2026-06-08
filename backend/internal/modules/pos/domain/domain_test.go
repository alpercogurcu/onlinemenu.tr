package domain_test

import (
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
