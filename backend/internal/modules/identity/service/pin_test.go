package service_test

// Integration tests for PinService (ADR-DATA-008 PIN akışı), reusing the
// shared testcontainers harness declared in membership_service_test.go
// (sharedPool, tenantA, branchA — see that file's TestMain).

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	pub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/identity/repo"
	"onlinemenu.tr/internal/modules/identity/service"
)

// tenantB is a second tenant scope used only by TestPinService_TenantIsolation.
// cashier_pins carries no FK to tenants (see migration 000015's comment on
// why — RLS is the only enforcement this table needs), so this UUID never
// needs a row in the tenants table to be a valid, isolated app.tenant_id scope.
var tenantB = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000099")

func newPinService() *service.PinService {
	return service.NewPinService(service.PinParams{
		DB: sharedPool, Pins: repo.NewCashierPinRepo(), Logger: zap.NewNop(),
	})
}

func TestPinService_SetVerify_RoundTrip(t *testing.T) {
	svc := newPinService()
	ctx := t.Context()
	personID := uuid.New()

	require.NoError(t, svc.SetOwnPin(ctx, tenantA, personID, "1234"))

	assert.NoError(t, svc.VerifyPin(ctx, tenantA, personID, "1234"))
	err := svc.VerifyPin(ctx, tenantA, personID, "9999")
	assert.ErrorIs(t, err, pub.ErrPinVerificationFailed)
}

func TestPinService_VerifyPin_NoPinSet_SameErrorAsWrongPin(t *testing.T) {
	svc := newPinService()
	ctx := t.Context()

	// Two different persons: one has genuinely never set a PIN, the other
	// exists purely as a garbage UUID with no row anywhere. Both must
	// produce the exact same sentinel — this is the enumeration-safety
	// contract identity/public.CashierPinService documents.
	neverSetPersonID := uuid.New()
	garbagePersonID := uuid.New()

	err1 := svc.VerifyPin(ctx, tenantA, neverSetPersonID, "1234")
	err2 := svc.VerifyPin(ctx, tenantA, garbagePersonID, "1234")

	assert.ErrorIs(t, err1, pub.ErrPinVerificationFailed)
	assert.ErrorIs(t, err2, pub.ErrPinVerificationFailed)
	assert.Equal(t, err1, err2, "both must be the identical sentinel value, not merely errors.Is-compatible ones")
}

func TestPinService_SetOwnPin_RejectsBadFormat(t *testing.T) {
	svc := newPinService()
	ctx := t.Context()

	err := svc.SetOwnPin(ctx, tenantA, uuid.New(), "12")
	assert.ErrorIs(t, err, pub.ErrPinFormatInvalid)
}

func TestPinService_SetOwnPin_Replaces(t *testing.T) {
	svc := newPinService()
	ctx := t.Context()
	personID := uuid.New()

	require.NoError(t, svc.SetOwnPin(ctx, tenantA, personID, "1111"))
	require.NoError(t, svc.SetOwnPin(ctx, tenantA, personID, "2222"))

	assert.ErrorIs(t, svc.VerifyPin(ctx, tenantA, personID, "1111"), pub.ErrPinVerificationFailed,
		"old pin must no longer verify after replacement")
	assert.NoError(t, svc.VerifyPin(ctx, tenantA, personID, "2222"))
}

func TestPinService_ResetPin_ClearsIt(t *testing.T) {
	svc := newPinService()
	ctx := t.Context()
	personID := uuid.New()

	require.NoError(t, svc.SetOwnPin(ctx, tenantA, personID, "1234"))
	require.NoError(t, svc.VerifyPin(ctx, tenantA, personID, "1234"))

	require.NoError(t, svc.ResetPin(ctx, tenantA, personID))

	err := svc.VerifyPin(ctx, tenantA, personID, "1234")
	assert.ErrorIs(t, err, pub.ErrPinVerificationFailed, "the correct former pin must no longer verify after reset")
}

func TestPinService_ResetPin_UnsetIsNotAnError(t *testing.T) {
	svc := newPinService()
	ctx := t.Context()

	assert.NoError(t, svc.ResetPin(ctx, tenantA, uuid.New()))
}

// TestPinService_TenantIsolation proves the (person, tenant) scope from
// ADR-DATA-008 §1: the same personID can hold two different PINs in two
// different tenants, and one tenant's VerifyPin must not see the other's row.
func TestPinService_TenantIsolation(t *testing.T) {
	svc := newPinService()
	ctx := t.Context()
	personID := uuid.New()

	require.NoError(t, svc.SetOwnPin(ctx, tenantA, personID, "1111"))
	require.NoError(t, svc.SetOwnPin(ctx, tenantB, personID, "2222"))

	assert.NoError(t, svc.VerifyPin(ctx, tenantA, personID, "1111"))
	assert.NoError(t, svc.VerifyPin(ctx, tenantB, personID, "2222"))
	assert.ErrorIs(t, svc.VerifyPin(ctx, tenantA, personID, "2222"), pub.ErrPinVerificationFailed,
		"tenant B's pin must not verify under tenant A's scope")
}
