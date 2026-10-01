package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
	"onlinemenu.tr/internal/modules/pos/service"
)

// ---------------------------------------------------------------------------
// Service type (gel al / paket) tests — pos/000010
// ---------------------------------------------------------------------------

// TestCheckService_Open_DineIn_BackwardCompatible proves a request shaped
// exactly like every pre-000010 Open call — no service_type, no customer
// fields — still opens an ordinary dine_in check, with the persisted row
// (not just the Create response) carrying the defaults.
func TestCheckService_Open_DineIn_BackwardCompatible(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()

	c, err := svc.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:   branchA,
		TableLabel: "Masa Dine-in Uyum",
		OpenedBy:   &staffA,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.ServiceTypeDineIn, c.ServiceType)

	fetched, err := svc.GetByID(ctx, tenantA, c.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ServiceTypeDineIn, fetched.ServiceType)
	assert.Empty(t, fetched.CustomerName)
	assert.Empty(t, fetched.CustomerPhone)
	assert.Empty(t, fetched.CustomerAddress)
	assert.Equal(t, "Masa Dine-in Uyum", fetched.TableLabel)
}

// TestCheckService_Open_Takeaway_RejectsTableID: a takeaway check never sits
// on the floor plan, so naming a table_id must be a validation error — not a
// silently dropped field, and not a claimed table.
func TestCheckService_Open_Takeaway_RejectsTableID(t *testing.T) {
	svc := newCheckService()
	tableID := uuid.New()

	_, err := svc.Open(chainWideCtx(t, context.Background()), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:     branchA,
		TableID:      &tableID,
		ServiceType:  domain.ServiceTypeTakeaway,
		CustomerName: "Alper Vural",
		OpenedBy:     &staffA,
	})
	assert.ErrorIs(t, err, service.ErrTableNotAllowed)
}

func TestCheckService_Open_Takeaway_RequiresCustomerName(t *testing.T) {
	svc := newCheckService()

	_, err := svc.Open(chainWideCtx(t, context.Background()), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:    branchA,
		ServiceType: domain.ServiceTypeTakeaway,
		OpenedBy:    &staffA,
	})
	assert.ErrorIs(t, err, service.ErrCustomerNameRequired)
}

// TestCheckService_Open_Takeaway_LabelFilledFromCustomerName is the contract
// that keeps KDS, kitchen receipts and every other table_label consumer
// working with zero changes: a takeaway check with no label renders as the
// customer's name wherever a table name would have appeared — and it must
// round-trip through GetByID, not just the Create response.
func TestCheckService_Open_Takeaway_LabelFilledFromCustomerName(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()

	c, err := svc.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:      branchA,
		ServiceType:   domain.ServiceTypeTakeaway,
		CustomerName:  "Alper Vural",
		CustomerPhone: "05321112233",
		OpenedBy:      &staffA,
	})
	require.NoError(t, err)
	assert.Equal(t, "Alper Vural", c.TableLabel)
	assert.Nil(t, c.TableID)

	fetched, err := svc.GetByID(ctx, tenantA, c.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ServiceTypeTakeaway, fetched.ServiceType)
	assert.Equal(t, "Alper Vural", fetched.TableLabel)
	assert.Equal(t, "Alper Vural", fetched.CustomerName)
	assert.Equal(t, "05321112233", fetched.CustomerPhone)
}

func TestCheckService_Open_Delivery_RequiresCustomerPhone(t *testing.T) {
	svc := newCheckService()

	_, err := svc.Open(chainWideCtx(t, context.Background()), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:     branchA,
		ServiceType:  domain.ServiceTypeDelivery,
		CustomerName: "Alper Vural",
		OpenedBy:     &staffA,
	})
	assert.ErrorIs(t, err, service.ErrCustomerPhoneRequired)
}

// TestCheckService_Open_Delivery_PersistsCustomerFields covers the full
// delivery payload (name + phone + address) persisting and round-tripping.
func TestCheckService_Open_Delivery_PersistsCustomerFields(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()

	c, err := svc.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:        branchA,
		ServiceType:     domain.ServiceTypeDelivery,
		CustomerName:    "Ayşe Yılmaz",
		CustomerPhone:   "05339998877",
		CustomerAddress: "Çark Cad. No:12 D:3 Serdivan",
		OpenedBy:        &staffA,
	})
	require.NoError(t, err)

	fetched, err := svc.GetByID(ctx, tenantA, c.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ServiceTypeDelivery, fetched.ServiceType)
	assert.Equal(t, "Ayşe Yılmaz", fetched.CustomerName)
	assert.Equal(t, "05339998877", fetched.CustomerPhone)
	assert.Equal(t, "Çark Cad. No:12 D:3 Serdivan", fetched.CustomerAddress)
	assert.Equal(t, "Ayşe Yılmaz", fetched.TableLabel)
}

func TestCheckService_Open_InvalidServiceType_Rejected(t *testing.T) {
	svc := newCheckService()

	_, err := svc.Open(chainWideCtx(t, context.Background()), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:    branchA,
		ServiceType: "drive_thru",
		OpenedBy:    &staffA,
	})
	assert.ErrorIs(t, err, service.ErrInvalidServiceType)
}

// TestCheckService_List_FilterByServiceType: the new optional list filter
// must return exactly the matching checks and, when absent, keep returning
// everything — mirroring the status/branch_id filters' nil-is-no-op
// convention.
func TestCheckService_List_FilterByServiceType(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()

	dineIn := openTestCheck(t, ctx, svc)
	takeaway, err := svc.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:     branchA,
		ServiceType:  domain.ServiceTypeTakeaway,
		CustomerName: "Filtre Takeaway",
		OpenedBy:     &staffA,
	})
	require.NoError(t, err)

	st := domain.ServiceTypeTakeaway
	filtered, _, err := svc.List(ctx, tenantA, service.CheckListFilter{ServiceType: &st})
	require.NoError(t, err)

	filteredIDs := make([]uuid.UUID, len(filtered))
	for i, c := range filtered {
		require.Equal(t, domain.ServiceTypeTakeaway, c.ServiceType, "filtered list must only contain takeaway checks")
		filteredIDs[i] = c.ID
	}
	assert.Contains(t, filteredIDs, takeaway.ID)
	assert.NotContains(t, filteredIDs, dineIn.ID)

	all, _, err := svc.List(ctx, tenantA, service.CheckListFilter{})
	require.NoError(t, err)
	allIDs := make([]uuid.UUID, len(all))
	for i, c := range all {
		allIDs[i] = c.ID
	}
	assert.Contains(t, allIDs, takeaway.ID)
	assert.Contains(t, allIDs, dineIn.ID)
}
