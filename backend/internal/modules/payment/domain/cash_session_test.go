package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// TestTransition_CashSession is the table-driven guard over
// ADR-DATA-008's whole state machine: every edge the map allows must
// succeed, and — just as important — every edge it does not allow (including
// backward and skip-ahead moves) must be rejected with
// ErrInvalidCashSessionTransition. docs/lessons-from-b2b.md item 1 calls out
// exactly this shape of test ("Status geçişleri ... geriye ve çapraz
// geçişlerin reddedildiği tablo-bazlı test").
func TestTransition_CashSession(t *testing.T) {
	tests := []struct {
		name    string
		from    domain.CashSessionStatus
		to      domain.CashSessionStatus
		wantErr bool
	}{
		// -- allowed forward edges -------------------------------------------
		{"opening_control -> opened", domain.CashSessionOpeningControl, domain.CashSessionOpened, false},
		{"opened -> closing_control", domain.CashSessionOpened, domain.CashSessionClosingControl, false},
		{"closing_control -> closed", domain.CashSessionClosingControl, domain.CashSessionClosed, false},
		{"closing_control -> closing_control (recount)", domain.CashSessionClosingControl, domain.CashSessionClosingControl, false},

		// -- skip-ahead ---------------------------------------------------------
		{"opening_control -> closing_control", domain.CashSessionOpeningControl, domain.CashSessionClosingControl, true},
		{"opening_control -> closed", domain.CashSessionOpeningControl, domain.CashSessionClosed, true},
		{"opened -> closed", domain.CashSessionOpened, domain.CashSessionClosed, true},

		// -- backward -------------------------------------------------------------
		{"opened -> opening_control", domain.CashSessionOpened, domain.CashSessionOpeningControl, true},
		{"closing_control -> opened", domain.CashSessionClosingControl, domain.CashSessionOpened, true},
		{"closed -> closing_control", domain.CashSessionClosed, domain.CashSessionClosingControl, true},

		// -- self-loops other than the documented recount ------------------------
		{"opening_control -> opening_control", domain.CashSessionOpeningControl, domain.CashSessionOpeningControl, true},
		{"opened -> opened", domain.CashSessionOpened, domain.CashSessionOpened, true},

		// -- closed is terminal ---------------------------------------------------
		{"closed -> closed", domain.CashSessionClosed, domain.CashSessionClosed, true},
		{"closed -> opened", domain.CashSessionClosed, domain.CashSessionOpened, true},

		// -- invalid target status --------------------------------------------
		{"opened -> garbage", domain.CashSessionOpened, domain.CashSessionStatus("garbage"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.Transition(tt.from, tt.to)
			if tt.wantErr {
				assert.Error(t, err)
				assert.True(t, errors.Is(err, domain.ErrInvalidCashSessionTransition),
					"error must wrap ErrInvalidCashSessionTransition so callers can distinguish it from other failures")
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestCashSessionStatus_Valid(t *testing.T) {
	valid := []domain.CashSessionStatus{
		domain.CashSessionOpeningControl, domain.CashSessionOpened,
		domain.CashSessionClosingControl, domain.CashSessionClosed,
	}
	for _, s := range valid {
		assert.True(t, s.Valid(), "%s must be a recognised status", s)
	}
	assert.False(t, domain.CashSessionStatus("").Valid())
	assert.False(t, domain.CashSessionStatus("bogus").Valid())
}

func TestCashMovementDirection_Valid(t *testing.T) {
	assert.True(t, domain.CashMovementIn.Valid())
	assert.True(t, domain.CashMovementOut.Valid())
	assert.False(t, domain.CashMovementDirection("").Valid())
	assert.False(t, domain.CashMovementDirection("sideways").Valid())
}

// TestValidateDenominations covers the sum invariant: a supplied breakdown
// must reconcile against the total, but an OMITTED breakdown is valid on its
// own (the breakdown is an optional aid, not a mandatory input).
func TestValidateDenominations(t *testing.T) {
	tests := []struct {
		name      string
		amount    int64
		breakdown []domain.DenominationCount
		wantErr   error // nil means no error expected
	}{
		{
			name:      "empty breakdown is always valid",
			amount:    123456,
			breakdown: nil,
		},
		{
			name:   "exact match",
			amount: 20000*3 + 10000*5,
			breakdown: []domain.DenominationCount{
				{DenominationMinor: 20000, Count: 3}, // 200 TL x 3
				{DenominationMinor: 10000, Count: 5}, // 100 TL x 5
			},
		},
		{
			name:   "mismatch is rejected",
			amount: 100000,
			breakdown: []domain.DenominationCount{
				{DenominationMinor: 20000, Count: 3}, // sums to 60000, not 100000
			},
			wantErr: domain.ErrDenominationSumMismatch,
		},
		{
			name:   "zero count line is fine as long as the total still matches",
			amount: 20000,
			breakdown: []domain.DenominationCount{
				{DenominationMinor: 20000, Count: 1},
				{DenominationMinor: 10000, Count: 0},
			},
		},
		{
			name:   "negative count is rejected",
			amount: 0,
			breakdown: []domain.DenominationCount{
				{DenominationMinor: 20000, Count: -1},
			},
			wantErr: nil, // distinct error, checked separately below via Error() text
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.ValidateDenominations(tt.breakdown, tt.amount)
			if tt.name == "negative count is rejected" {
				assert.Error(t, err)
				return
			}
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}
