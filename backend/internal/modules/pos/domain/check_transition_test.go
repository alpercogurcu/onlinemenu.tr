package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// TestTransitionCheckStatus is the table-driven regression test for
// docs/lessons-from-b2b.md item 2's status bullet, applied to the adisyon
// machine: every backward and cross edge must be rejected, and the three
// terminal statuses must have no way out.
//
// It is deliberately exhaustive over the full status × status matrix rather
// than a hand-picked list of interesting pairs — a new status constant added
// to check.go without an entry in allowedCheckTransitions shows up here as a
// failing "unexpected edge" case instead of quietly becoming reachable.
func TestTransitionCheckStatus(t *testing.T) {
	all := []domain.CheckStatus{
		domain.CheckStatusOpen,
		domain.CheckStatusClosed,
		domain.CheckStatusCancelled,
		domain.CheckStatusMerged,
	}

	// The complete set of legal edges. Anything not listed must be refused.
	legal := map[domain.CheckStatus]map[domain.CheckStatus]bool{
		domain.CheckStatusOpen: {
			domain.CheckStatusClosed:    true,
			domain.CheckStatusCancelled: true,
			domain.CheckStatusMerged:    true,
		},
	}

	for _, from := range all {
		for _, to := range all {
			from, to := from, to
			t.Run(string(from)+"->"+string(to), func(t *testing.T) {
				err := domain.TransitionCheckStatus(from, to)
				if legal[from][to] {
					require.NoErrorf(t, err, "%s -> %s must be allowed", from, to)
					return
				}
				require.Errorf(t, err, "%s -> %s must be rejected", from, to)
				assert.ErrorIsf(t, err, domain.ErrInvalidTransition,
					"%s -> %s must fail with ErrInvalidTransition so callers can map it to 409", from, to)
			})
		}
	}
}

// TestTransitionCheckStatus_TerminalStatusesAreSinks states the property the
// matrix above proves, on its own, so the intent survives a future edit to
// the table: once an adisyon is closed, cancelled or merged it can never move
// again. A reopened closed check would re-enter a business day that has
// already been reported (ADR-DATA-003); a reopened merged check would
// double-count orders that now hang off another check.
func TestTransitionCheckStatus_TerminalStatusesAreSinks(t *testing.T) {
	terminal := []domain.CheckStatus{
		domain.CheckStatusClosed,
		domain.CheckStatusCancelled,
		domain.CheckStatusMerged,
	}
	targets := append([]domain.CheckStatus{domain.CheckStatusOpen}, terminal...)

	for _, from := range terminal {
		for _, to := range targets {
			err := domain.TransitionCheckStatus(from, to)
			assert.Truef(t, errors.Is(err, domain.ErrInvalidTransition),
				"%s is terminal but %s -> %s was allowed", from, from, to)
		}
	}
}

// TestTransitionCheckStatus_RejectsUnknownTarget guards the other direction:
// a status string that is not a CheckStatus constant at all (a typo in a
// caller, or a value read back from an older row) must never be accepted.
func TestTransitionCheckStatus_RejectsUnknownTarget(t *testing.T) {
	err := domain.TransitionCheckStatus(domain.CheckStatusOpen, domain.CheckStatus("paid"))
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrInvalidTransition)
}
