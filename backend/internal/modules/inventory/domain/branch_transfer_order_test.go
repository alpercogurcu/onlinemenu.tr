package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"onlinemenu.tr/internal/modules/inventory/domain"
)

func TestBTOStatus_Valid(t *testing.T) {
	valid := []domain.BTOStatus{
		domain.BTOStatusDraft, domain.BTOStatusSubmitted, domain.BTOStatusApproved,
		domain.BTOStatusFulfilling, domain.BTOStatusShipped, domain.BTOStatusReceived,
		domain.BTOStatusClosed, domain.BTOStatusRejected, domain.BTOStatusCancelled,
	}
	for _, s := range valid {
		assert.True(t, s.Valid(), "expected %q to be valid", s)
	}
	assert.False(t, domain.BTOStatus("unknown").Valid())
}

// TestTransitionBTOStatus_Matrix asserts every (from, to) pair against the
// ADR-DATA-006 state diagram. This is the single source of truth: any future
// edit to allowedBTOTransitions must be reflected here (lessons-from-b2b item
// 2 — one allowedTransitions map, one exhaustive regression test).
func TestTransitionBTOStatus_Matrix(t *testing.T) {
	allStatuses := []domain.BTOStatus{
		domain.BTOStatusDraft, domain.BTOStatusSubmitted, domain.BTOStatusApproved,
		domain.BTOStatusFulfilling, domain.BTOStatusShipped, domain.BTOStatusReceived,
		domain.BTOStatusClosed, domain.BTOStatusRejected, domain.BTOStatusCancelled,
	}

	allowed := map[domain.BTOStatus]map[domain.BTOStatus]bool{
		domain.BTOStatusDraft: {
			domain.BTOStatusSubmitted: true,
			domain.BTOStatusCancelled: true,
		},
		domain.BTOStatusSubmitted: {
			domain.BTOStatusApproved:  true,
			domain.BTOStatusRejected:  true,
			domain.BTOStatusCancelled: true,
		},
		domain.BTOStatusApproved: {
			domain.BTOStatusFulfilling: true,
			domain.BTOStatusCancelled:  true,
		},
		domain.BTOStatusFulfilling: {
			domain.BTOStatusShipped: true,
		},
		domain.BTOStatusShipped: {
			domain.BTOStatusReceived: true,
		},
		domain.BTOStatusReceived: {
			domain.BTOStatusClosed: true,
		},
		// closed, rejected, cancelled are terminal: no entries.
	}

	for _, from := range allStatuses {
		for _, to := range allStatuses {
			from, to := from, to
			wantOK := allowed[from][to]
			t.Run(string(from)+"->"+string(to), func(t *testing.T) {
				err := domain.TransitionBTOStatus(from, to)
				if wantOK {
					assert.NoError(t, err)
				} else {
					assert.Error(t, err)
					assert.True(t, errors.Is(err, domain.ErrInvalidBTOTransition))
				}
			})
		}
	}
}

func TestTransitionBTOStatus_RejectsUnknownTarget(t *testing.T) {
	err := domain.TransitionBTOStatus(domain.BTOStatusDraft, domain.BTOStatus("in_progress"))
	assert.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidBTOTransition))
}

// TestTransitionBTOStatus_ReceivedNeverCallerDriven documents the ADR-DATA-006
// ownership rule at the type level: 'received' is only reachable from
// 'shipped', which itself is only reachable from 'fulfilling'. There is no
// edge from draft/submitted/approved directly to shipped/received — a caller
// cannot skip the shipment-driven path.
func TestTransitionBTOStatus_ReceivedOnlyFromShipped(t *testing.T) {
	for _, from := range []domain.BTOStatus{
		domain.BTOStatusDraft, domain.BTOStatusSubmitted, domain.BTOStatusApproved,
		domain.BTOStatusFulfilling, domain.BTOStatusRejected, domain.BTOStatusCancelled,
		domain.BTOStatusClosed,
	} {
		err := domain.TransitionBTOStatus(from, domain.BTOStatusReceived)
		assert.Errorf(t, err, "expected %s -> received to be rejected", from)
	}
	assert.NoError(t, domain.TransitionBTOStatus(domain.BTOStatusShipped, domain.BTOStatusReceived))
}
