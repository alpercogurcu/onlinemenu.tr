package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	catalogpub "onlinemenu.tr/internal/modules/catalog/public"
	pospub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/storefront/domain"
	pub "onlinemenu.tr/internal/modules/storefront/public"
	"onlinemenu.tr/internal/modules/storefront/repo"
	"onlinemenu.tr/internal/modules/storefront/service"
	"onlinemenu.tr/internal/platform/auth"
)

// fakeGate is the OrderingGate stub every unit-level OrderService test wires
// in; the integration tests below use the real BranchSettingsService instead.
type fakeGate struct {
	enabled bool
	err     error
	calls   int
}

func (f *fakeGate) OrderingEnabled(_ context.Context, _, _ uuid.UUID) (bool, error) {
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	return f.enabled, nil
}

// TestPlace_OrderingDisabled_RefusesBeforePricing is the service half of the
// branch kill switch: a disabled gate must short-circuit Place before the
// cart is priced or pos is touched, with the sentinel the HTTP layer maps to
// 409 ordering_disabled.
func TestPlace_OrderingDisabled_RefusesBeforePricing(t *testing.T) {
	catalog := &fakeCatalog{}
	pos := &fakePos{}
	gate := &fakeGate{enabled: false}
	svc := service.NewOrderService(service.OrderParams{
		Catalog: catalog,
		Placer:  pos,
		Orders:  pos,
		Gate:    gate,
		Logger:  zap.NewNop(),
	})

	_, err := svc.Place(context.Background(), testGuest(), domain.GuestCart{
		Lines: []domain.GuestCartLine{{ProductID: uuid.New(), Quantity: 1}},
	})

	require.ErrorIs(t, err, pub.ErrOrderingDisabled)
	assert.Equal(t, 1, gate.calls)
	assert.Nil(t, catalog.gotLines, "a refused cart must never be priced")
	assert.Empty(t, pos.got.Lines, "a refused cart must never reach pos")
}

// TestPlace_GateReadFails_IsAnErrorNotAnOpenDoor: an unreadable settings row
// must fail the placement, not silently fall back to "enabled" — the fallback
// to the default belongs to "no row exists", never to "the read broke".
func TestPlace_GateReadFails_IsAnErrorNotAnOpenDoor(t *testing.T) {
	catalog := &fakeCatalog{}
	pos := &fakePos{}
	gateErr := errors.New("settings read failed")
	svc := service.NewOrderService(service.OrderParams{
		Catalog: catalog,
		Placer:  pos,
		Orders:  pos,
		Gate:    &fakeGate{err: gateErr},
		Logger:  zap.NewNop(),
	})

	_, err := svc.Place(context.Background(), testGuest(), domain.GuestCart{
		Lines: []domain.GuestCartLine{{ProductID: uuid.New(), Quantity: 1}},
	})

	require.ErrorIs(t, err, gateErr)
	assert.Empty(t, pos.got.Lines)
}

// ---------------------------------------------------------------------------
// Integration: real storefront_branch_settings rows under RLS (sharedPool is
// the module-wide testcontainers Postgres from session_integration_test.go).
// ---------------------------------------------------------------------------

func newBranchSettingsService() *service.BranchSettingsService {
	return service.NewBranchSettingsService(service.BranchSettingsParams{
		DB:       sharedPool,
		Settings: repo.NewBranchSettingsRepo(),
		Logger:   zap.NewNop(),
	})
}

// branchPrincipal satisfies requireBranch's direct-match arm without planting
// an OPA scope, like seedQRCode's fixture.
func branchPrincipal(tenantID, branchID uuid.UUID) auth.Principal {
	return auth.Principal{
		Ctx: auth.ContextStaff, TenantID: tenantID, BranchID: branchID, PersonID: uuid.New(),
	}
}

// TestBranchSettings_NoRow_DefaultsToEnabled pins the backfill-free rollout:
// a branch nobody ever configured answers "enabled" on both surfaces and no
// row is created by reading.
func TestBranchSettings_NoRow_DefaultsToEnabled(t *testing.T) {
	tenantID, branchID := uuid.New(), uuid.New()
	svc := newBranchSettingsService()

	settings, err := svc.Get(context.Background(), tenantID, branchPrincipal(tenantID, branchID), branchID)
	require.NoError(t, err)
	assert.True(t, settings.OrderingEnabled)
	assert.Equal(t, branchID, settings.BranchID)

	enabled, err := svc.OrderingEnabled(context.Background(), tenantID, branchID)
	require.NoError(t, err)
	assert.True(t, enabled)

	// Reading twice must still find no row — Get is side-effect free, so the
	// second admin GET keeps answering from the defaults, not from a row the
	// first GET would have minted with a bogus updated_by.
	settings, err = svc.Get(context.Background(), tenantID, branchPrincipal(tenantID, branchID), branchID)
	require.NoError(t, err)
	assert.True(t, settings.OrderingEnabled)
}

// TestBranchSettings_SetRoundTrip walks the admin surface end to end: disable
// (creates the row), read it back, re-enable (updates in place).
func TestBranchSettings_SetRoundTrip(t *testing.T) {
	tenantID, branchID := uuid.New(), uuid.New()
	principal := branchPrincipal(tenantID, branchID)
	svc := newBranchSettingsService()

	disabled, err := svc.Set(context.Background(), tenantID, principal, service.SetRequest{
		BranchID: branchID, OrderingEnabled: false, UpdatedBy: principal.PersonID,
	})
	require.NoError(t, err)
	assert.False(t, disabled.OrderingEnabled)

	settings, err := svc.Get(context.Background(), tenantID, principal, branchID)
	require.NoError(t, err)
	assert.False(t, settings.OrderingEnabled)

	enabled, err := svc.OrderingEnabled(context.Background(), tenantID, branchID)
	require.NoError(t, err)
	assert.False(t, enabled)

	reenabled, err := svc.Set(context.Background(), tenantID, principal, service.SetRequest{
		BranchID: branchID, OrderingEnabled: true, UpdatedBy: principal.PersonID,
	})
	require.NoError(t, err)
	assert.True(t, reenabled.OrderingEnabled)

	enabled, err = svc.OrderingEnabled(context.Background(), tenantID, branchID)
	require.NoError(t, err)
	assert.True(t, enabled)
}

// TestBranchSettings_OtherBranchStaff_IsForbidden is the SEC-005 guard: a
// branch-scoped principal may neither read nor flip another branch's switch,
// exactly as the QR endpoints enforce it.
func TestBranchSettings_OtherBranchStaff_IsForbidden(t *testing.T) {
	tenantID := uuid.New()
	ownBranch, otherBranch := uuid.New(), uuid.New()
	principal := branchPrincipal(tenantID, ownBranch)
	svc := newBranchSettingsService()

	_, err := svc.Get(context.Background(), tenantID, principal, otherBranch)
	assert.ErrorIs(t, err, pub.ErrBranchForbidden)

	_, err = svc.Set(context.Background(), tenantID, principal, service.SetRequest{
		BranchID: otherBranch, OrderingEnabled: false, UpdatedBy: principal.PersonID,
	})
	assert.ErrorIs(t, err, pub.ErrBranchForbidden)
}

// TestBranchSettings_CrossTenant_RowIsInvisible: tenant B reading the very
// branch id tenant A disabled sees the default, because A's row is outside
// B's RLS context. (Branch ids never collide across tenants in practice —
// this pins what the policy does if one ever did.)
func TestBranchSettings_CrossTenant_RowIsInvisible(t *testing.T) {
	tenantA, tenantB, branchID := uuid.New(), uuid.New(), uuid.New()
	svc := newBranchSettingsService()

	_, err := svc.Set(context.Background(), tenantA, branchPrincipal(tenantA, branchID), service.SetRequest{
		BranchID: branchID, OrderingEnabled: false, UpdatedBy: uuid.New(),
	})
	require.NoError(t, err)

	enabled, err := svc.OrderingEnabled(context.Background(), tenantB, branchID)
	require.NoError(t, err)
	assert.True(t, enabled, "another tenant's settings row must not leak through RLS")
}

// TestPlace_DisabledBranch_EndToEnd runs the enforcement against the real
// settings row: the same OrderService wiring the visibility tests use, with
// the real gate, refuses the cart once the branch is switched off.
func TestPlace_DisabledBranch_EndToEnd(t *testing.T) {
	seed := seedQRCode(t, uuid.New())
	guest := guestSessionFor(t, seed)
	settingsSvc := newBranchSettingsService()

	_, err := settingsSvc.Set(context.Background(), guest.TenantID,
		branchPrincipal(guest.TenantID, guest.BranchID), service.SetRequest{
			BranchID: guest.BranchID, OrderingEnabled: false, UpdatedBy: uuid.New(),
		})
	require.NoError(t, err)

	placer := &linkingPlacer{tenantID: guest.TenantID, orderID: uuid.New(), checkID: uuid.New()}
	svc := service.NewOrderService(service.OrderParams{
		DB: sharedPool,
		Catalog: staticCatalog{line: catalogpub.PricedLine{
			ProductName: "Çay", BasePriceAmount: 1000, UnitPriceAmount: 1000,
			Currency: "TRY", TaxRateBPS: 1000,
		}},
		Placer:      placer,
		Orders:      &mapOrderReader{views: map[uuid.UUID]pospub.GuestOrderView{}},
		GuestOrders: repo.NewGuestOrderRepo(),
		Gate:        settingsSvc,
		Logger:      zap.NewNop(),
	})

	_, err = svc.Place(context.Background(), guest, domain.GuestCart{
		Lines: []domain.GuestCartLine{{ProductID: uuid.New(), Quantity: 1}},
	})
	require.ErrorIs(t, err, pub.ErrOrderingDisabled)
}
