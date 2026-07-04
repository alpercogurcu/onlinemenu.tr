package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"onlinemenu.tr/internal/modules/inventory/domain"
)

func TestShipmentStatus_Valid(t *testing.T) {
	valid := []domain.ShipmentStatus{
		domain.ShipmentStatusDraft, domain.ShipmentStatusApproved, domain.ShipmentStatusInTransit,
		domain.ShipmentStatusReceived, domain.ShipmentStatusCancelled,
	}
	for _, s := range valid {
		assert.True(t, s.Valid(), "expected %q to be valid", s)
	}
	assert.False(t, domain.ShipmentStatus("unknown").Valid())
}

// TestTransitionShipmentStatus_Matrix asserts every (from, to) pair against
// the ADR-DATA-006 SHIPMENTS state machine.
func TestTransitionShipmentStatus_Matrix(t *testing.T) {
	allStatuses := []domain.ShipmentStatus{
		domain.ShipmentStatusDraft, domain.ShipmentStatusApproved, domain.ShipmentStatusInTransit,
		domain.ShipmentStatusReceived, domain.ShipmentStatusCancelled,
	}

	allowed := map[domain.ShipmentStatus]map[domain.ShipmentStatus]bool{
		domain.ShipmentStatusDraft: {
			domain.ShipmentStatusApproved:  true,
			domain.ShipmentStatusCancelled: true,
		},
		domain.ShipmentStatusApproved: {
			domain.ShipmentStatusInTransit: true,
			domain.ShipmentStatusCancelled: true,
		},
		domain.ShipmentStatusInTransit: {
			domain.ShipmentStatusReceived: true,
		},
		// received, cancelled are terminal: no entries.
	}

	for _, from := range allStatuses {
		for _, to := range allStatuses {
			from, to := from, to
			wantOK := allowed[from][to]
			t.Run(string(from)+"->"+string(to), func(t *testing.T) {
				err := domain.TransitionShipmentStatus(from, to)
				if wantOK {
					assert.NoError(t, err)
				} else {
					assert.Error(t, err)
					assert.True(t, errors.Is(err, domain.ErrInvalidShipmentTransition))
				}
			})
		}
	}
}

func TestTransitionShipmentStatus_RejectsUnknownTarget(t *testing.T) {
	err := domain.TransitionShipmentStatus(domain.ShipmentStatusDraft, domain.ShipmentStatus("lost"))
	assert.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidShipmentTransition))
}

func TestPriority_Valid(t *testing.T) {
	assert.True(t, domain.PriorityNormal.Valid())
	assert.True(t, domain.PriorityUrgent.Valid())
	assert.False(t, domain.Priority("critical").Valid())
}
