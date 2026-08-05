package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/storefront/domain"
)

func TestTransitionQRCodeStatus(t *testing.T) {
	tests := []struct {
		name    string
		from    domain.QRCodeStatus
		to      domain.QRCodeStatus
		wantErr bool
	}{
		{name: "active to revoked", from: domain.QRCodeStatusActive, to: domain.QRCodeStatusRevoked},
		{
			name:    "revoked to active is one-way blocked",
			from:    domain.QRCodeStatusRevoked,
			to:      domain.QRCodeStatusActive,
			wantErr: true,
		},
		{
			name:    "revoked is terminal",
			from:    domain.QRCodeStatusRevoked,
			to:      domain.QRCodeStatusRevoked,
			wantErr: true,
		},
		{
			name:    "active to active is not a transition",
			from:    domain.QRCodeStatusActive,
			to:      domain.QRCodeStatusActive,
			wantErr: true,
		},
		{
			name:    "unknown target status",
			from:    domain.QRCodeStatusActive,
			to:      domain.QRCodeStatus("expired"),
			wantErr: true,
		},
		{
			name:    "unknown source status has no outgoing edges",
			from:    domain.QRCodeStatus(""),
			to:      domain.QRCodeStatusRevoked,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.TransitionQRCodeStatus(tt.from, tt.to)
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, domain.ErrInvalidTransition)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestQRCodeStatusValid(t *testing.T) {
	tests := []struct {
		status domain.QRCodeStatus
		want   bool
	}{
		{status: domain.QRCodeStatusActive, want: true},
		{status: domain.QRCodeStatusRevoked, want: true},
		{status: domain.QRCodeStatus(""), want: false},
		{status: domain.QRCodeStatus("ACTIVE"), want: false},
		{status: domain.QRCodeStatus("expired"), want: false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.status.Valid())
		})
	}
}

// TestQRCodeIsActive pins the helper the session service gates on: anything
// other than an explicitly active status must be treated as unusable, so a
// future status value cannot silently start minting guest sessions.
func TestQRCodeIsActive(t *testing.T) {
	assert.True(t, domain.QRCode{Status: domain.QRCodeStatusActive}.IsActive())
	assert.False(t, domain.QRCode{Status: domain.QRCodeStatusRevoked}.IsActive())
	assert.False(t, domain.QRCode{}.IsActive())
}
