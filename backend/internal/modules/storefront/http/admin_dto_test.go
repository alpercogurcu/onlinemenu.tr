package http

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/storefront/domain"
	"onlinemenu.tr/internal/modules/storefront/service"
)

// TestQRCodeResponse_NeverLeaksToken is the field-level projection guard
// (ADR-AUTH-001 layer 4) for the staff QR surface.
//
// domain.QRCode carries TokenHash. If someone ever "simplifies" qrCodeResponse
// into an embedded domain struct, or adds a passthrough field, list/get would
// start publishing the exact value stored in storefront_qr_codes' unique index
// — an offline oracle that turns a guessed token into a confirmed one without
// a single request to the guest API. This test fails the moment that happens.
func TestQRCodeResponse_NeverLeaksToken(t *testing.T) {
	code := domain.QRCode{
		ID:         uuid.New(),
		TenantID:   uuid.New(),
		BranchID:   uuid.New(),
		TableID:    uuid.New(),
		TableLabel: "Masa 1",
		TokenHash:  "0badc0ffee0badc0ffee0badc0ffee0badc0ffee0badc0ffee0badc0ffee0bad",
		Status:     domain.QRCodeStatusActive,
		CreatedBy:  uuid.New(),
	}

	raw, err := json.Marshal(toQRCodeResponse(code))
	require.NoError(t, err)
	body := string(raw)

	require.NotContains(t, body, code.TokenHash, "response body leaked the token hash")
	for _, forbidden := range []string{"token_hash", "tokenHash", "token"} {
		require.NotContainsf(t, body, forbidden,
			"qrCodeResponse must not carry a %q field — list/get are read-only views", forbidden)
	}
}

// TestIssuedQRCodeResponse_CarriesRawTokenOnly asserts the other half of the
// contract: create/rotate DO return the raw token (it exists nowhere else and
// the sticker cannot be printed without it), but still never the hash.
func TestIssuedQRCodeResponse_CarriesRawTokenOnly(t *testing.T) {
	issued := service.IssuedQRCode{
		Code: domain.QRCode{
			ID:        uuid.New(),
			TokenHash: "0badc0ffee0badc0ffee0badc0ffee0badc0ffee0badc0ffee0badc0ffee0bad",
			Status:    domain.QRCodeStatusActive,
		},
		RawToken: "vJ8-raw-token-value-only-ever-returned-once",
	}

	raw, err := json.Marshal(toIssuedQRCodeResponse(issued))
	require.NoError(t, err)
	body := string(raw)

	require.Contains(t, body, issued.RawToken)
	require.NotContains(t, body, issued.Code.TokenHash)
	require.False(t, strings.Contains(body, "token_hash"), "issued response must not carry token_hash")
}

// TestBranchSettingsResponse_Shape pins the settings wire contract the admin
// app is built against ({"branch_id":...,"ordering_enabled":...} and nothing
// else): audit fields and tenant_id stay server-side, and a field added to
// domain.BranchSettings must not leak here without a deliberate edit.
func TestBranchSettingsResponse_Shape(t *testing.T) {
	branchID := uuid.New()
	settings := domain.BranchSettings{
		TenantID:        uuid.New(),
		BranchID:        branchID,
		OrderingEnabled: false,
		UpdatedBy:       uuid.New(),
	}

	raw, err := json.Marshal(toBranchSettingsResponse(settings))
	require.NoError(t, err)

	require.JSONEq(t,
		`{"branch_id":"`+branchID.String()+`","ordering_enabled":false}`,
		string(raw))
	require.NotContains(t, string(raw), settings.TenantID.String())
	require.NotContains(t, string(raw), settings.UpdatedBy.String())
}

// TestBranchSettingsResponse_DefaultIsEnabled keeps the "no row means on"
// contract observable at the DTO level: the default the service falls back to
// must serialize as ordering_enabled true.
func TestBranchSettingsResponse_DefaultIsEnabled(t *testing.T) {
	branchID := uuid.New()
	raw, err := json.Marshal(toBranchSettingsResponse(domain.DefaultBranchSettings(uuid.New(), branchID)))
	require.NoError(t, err)
	require.JSONEq(t,
		`{"branch_id":"`+branchID.String()+`","ordering_enabled":true}`,
		string(raw))
}
