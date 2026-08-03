package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/identity/domain"
)

func TestValidatePinFormat(t *testing.T) {
	tests := []struct {
		name    string
		pin     string
		wantErr bool
	}{
		{"4 digits ok", "1234", false},
		{"6 digits ok", "123456", false},
		{"5 digits ok", "12345", false},
		{"3 digits too short", "123", true},
		{"7 digits too long", "1234567", true},
		{"empty", "", true},
		{"non-digit", "12a4", true},
		{"letters only", "abcd", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.ValidatePinFormat(tt.pin)
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, domain.ErrPinFormatInvalid)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestHashPin_VerifyPinHash_RoundTrip(t *testing.T) {
	salt, hash, err := domain.HashPin("4242")
	require.NoError(t, err)
	assert.NotEmpty(t, salt)
	assert.NotEmpty(t, hash)

	assert.True(t, domain.VerifyPinHash("4242", salt, hash), "correct pin must verify")
	assert.False(t, domain.VerifyPinHash("4241", salt, hash), "wrong pin must not verify")
	assert.False(t, domain.VerifyPinHash("42420", salt, hash), "different-length pin must not verify")
}

func TestHashPin_DistinctSaltsPerCall(t *testing.T) {
	salt1, hash1, err := domain.HashPin("1234")
	require.NoError(t, err)
	salt2, hash2, err := domain.HashPin("1234")
	require.NoError(t, err)

	assert.NotEqual(t, salt1, salt2, "salts must be fresh per call")
	assert.NotEqual(t, hash1, hash2, "same pin under different salts must hash differently")
}

func TestDummyPinCost_NeverVerifies(t *testing.T) {
	// DummyPinCost's return value is intentionally unused by callers — it
	// only exists for its CPU cost — but it must never accidentally return
	// true (which would be indistinguishable from "a real pin matched" if
	// some future caller started checking it).
	for _, pin := range []string{"0000", "1234", "999999"} {
		assert.False(t, domain.DummyPinCost(pin))
	}
}

// TestArgon2idCost_Measured is not an assertion — it exists to put a real
// wall-clock number for this module's argon2id parameters (m=64MiB, t=1,
// p=4) into the test log, per the task's request to measure rather than
// assume the params are cheap enough for a synchronous shift-change request.
func TestArgon2idCost_Measured(t *testing.T) {
	start := time.Now()
	const rounds = 5
	for i := 0; i < rounds; i++ {
		_, _, err := domain.HashPin("1234")
		require.NoError(t, err)
	}
	elapsed := time.Since(start) / rounds
	t.Logf("argon2id (m=64MiB, t=1, p=4) average hash time over %d rounds: %s", rounds, elapsed)
}
