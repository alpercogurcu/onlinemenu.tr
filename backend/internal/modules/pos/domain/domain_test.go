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

func TestTableStatus_Valid(t *testing.T) {
	valid := []domain.TableStatus{
		domain.TableStatusEmpty,
		domain.TableStatusOccupied,
		domain.TableStatusReserved,
		domain.TableStatusCleaning,
	}
	for _, s := range valid {
		assert.True(t, s.Valid(), "expected %q to be valid", s)
	}
	assert.False(t, domain.TableStatus("dirty").Valid())
}

func TestTransitionTableStatus(t *testing.T) {
	allStatuses := []domain.TableStatus{
		domain.TableStatusEmpty,
		domain.TableStatusOccupied,
		domain.TableStatusReserved,
		domain.TableStatusCleaning,
	}

	allowed := map[domain.TableStatus]map[domain.TableStatus]bool{
		domain.TableStatusEmpty: {
			domain.TableStatusOccupied: true,
			domain.TableStatusReserved: true,
			domain.TableStatusCleaning: true,
		},
		domain.TableStatusOccupied: {
			domain.TableStatusCleaning: true,
			domain.TableStatusEmpty:    true,
		},
		domain.TableStatusReserved: {
			domain.TableStatusOccupied: true,
			domain.TableStatusEmpty:    true,
		},
		domain.TableStatusCleaning: {
			domain.TableStatusEmpty: true,
		},
	}

	for _, from := range allStatuses {
		for _, to := range allStatuses {
			from, to := from, to
			wantOK := allowed[from][to]
			t.Run(string(from)+"->"+string(to), func(t *testing.T) {
				err := domain.TransitionTableStatus(from, to)
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

func TestTransitionTableStatus_RejectsUnknownTarget(t *testing.T) {
	err := domain.TransitionTableStatus(domain.TableStatusEmpty, domain.TableStatus("dirty"))
	assert.ErrorIs(t, err, domain.ErrInvalidTransition)
}

// TestIsKitchenAdvanceTarget pins the /advance endpoint's target whitelist.
// accepted/rejected/cancelled are deliberately excluded: they have dedicated
// endpoints gated by stricter permissions (pos.order.accept / pos.order.reject),
// and letting /advance reach them was a privilege escalation for kitchen/bar.
func TestIsKitchenAdvanceTarget(t *testing.T) {
	tests := []struct {
		status domain.OrderStatus
		want   bool
	}{
		{domain.OrderStatusPreparing, true},
		{domain.OrderStatusReady, true},
		{domain.OrderStatusDelivered, true},
		{domain.OrderStatusPending, false},
		{domain.OrderStatusAccepted, false},
		{domain.OrderStatusRejected, false},
		{domain.OrderStatusCancelled, false},
		{domain.OrderStatus("bogus"), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			assert.Equal(t, tt.want, domain.IsKitchenAdvanceTarget(tt.status))
		})
	}
}

// TestTransitionOrderStatus_CancelledStillReachable guards against "fixing"
// the /advance escalation by deleting the cancelled edges from the state
// machine: the dedicated cancel endpoint and the check-cancel cascade both
// depend on them.
func TestTransitionOrderStatus_CancelledStillReachable(t *testing.T) {
	for _, from := range domain.KitchenActiveOrderStatuses {
		assert.NoErrorf(t, domain.TransitionOrderStatus(from, domain.OrderStatusCancelled), "%s -> cancelled", from)
	}
}
