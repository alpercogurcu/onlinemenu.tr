package repo_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/repo"
)

// newTerminal builds a registration payload with per-test unique identifiers.
// (vendor, terminal_serial) is globally unique across tenants, so a shared
// serial would couple these tests to each other.
func newTerminal(tenantID, branchID uuid.UUID, serial string) repo.FiscalTerminal {
	return repo.FiscalTerminal{
		TenantID:          tenantID,
		BranchID:          branchID,
		Vendor:            "tokenx",
		TerminalSerial:    serial,
		VendorMerchantRef: "M1",
		VendorBranchRef:   "B1",
		Label:             "Kasa 1",
		BasketMode:        "instant",
	}
}

func TestFiscalAdminRepo_UpsertTerminal_IsIdempotent(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewFiscalAdminRepo(sharedPool)

	tenantID, branchID, serial := uuid.New(), uuid.New(), "AV-"+uuid.NewString()

	first, err := r.UpsertTerminal(ctx, newTerminal(tenantID, branchID, serial))
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, first.ID)
	assert.Equal(t, "instant", first.BasketMode)
	assert.True(t, first.IsActive)

	// Re-scanning the same device QR must update the existing registration, not
	// fail and not create a second row — this is what makes POST idempotent
	// without an Idempotency-Key.
	again := newTerminal(tenantID, branchID, serial)
	again.Label = "Kasa 2"
	again.BasketMode = "list"

	second, err := r.UpsertTerminal(ctx, again)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID, "upsert must reuse the existing row")
	assert.Equal(t, "Kasa 2", second.Label)
	assert.Equal(t, "list", second.BasketMode)
	assert.False(t, second.UpdatedAt.Before(first.UpdatedAt), "updated_at must advance")

	list, err := r.ListTerminals(ctx, tenantID, branchID)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

// TestFiscalAdminRepo_UpsertTerminal_ReactivatesAndRebindsBranch covers re-pairing
// a device that was deactivated and moved to another branch of the same tenant.
func TestFiscalAdminRepo_UpsertTerminal_ReactivatesAndRebindsBranch(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewFiscalAdminRepo(sharedPool)

	tenantID, branchOne, branchTwo := uuid.New(), uuid.New(), uuid.New()
	serial := "AV-" + uuid.NewString()

	created, err := r.UpsertTerminal(ctx, newTerminal(tenantID, branchOne, serial))
	require.NoError(t, err)

	inactive := false
	_, err = r.UpdateTerminal(ctx, tenantID, created.ID, repo.TerminalPatch{IsActive: &inactive})
	require.NoError(t, err)

	rebound, err := r.UpsertTerminal(ctx, newTerminal(tenantID, branchTwo, serial))
	require.NoError(t, err)
	assert.Equal(t, created.ID, rebound.ID)
	assert.Equal(t, branchTwo, rebound.BranchID)
	assert.True(t, rebound.IsActive, "re-pairing an archived device must reactivate it")

	oldBranch, err := r.ListTerminals(ctx, tenantID, branchOne)
	require.NoError(t, err)
	assert.Empty(t, oldBranch)
}

// TestFiscalAdminRepo_UpsertTerminal_CrossTenantSerialRejected is the important
// one: (vendor, terminal_serial) is globally unique so an inbound webhook maps
// to exactly one tenant. A second tenant claiming the same physical device must
// be refused, never silently rebind it and start receiving another tenant's
// fiscal results.
func TestFiscalAdminRepo_UpsertTerminal_CrossTenantSerialRejected(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewFiscalAdminRepo(sharedPool)

	serial := "AV-" + uuid.NewString()
	owner, intruder := uuid.New(), uuid.New()

	_, err := r.UpsertTerminal(ctx, newTerminal(owner, uuid.New(), serial))
	require.NoError(t, err)

	_, err = r.UpsertTerminal(ctx, newTerminal(intruder, uuid.New(), serial))
	assert.ErrorIs(t, err, repo.ErrTerminalSerialTaken)
}

func TestFiscalAdminRepo_GetTerminal_NotFoundAndTenantIsolation(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewFiscalAdminRepo(sharedPool)

	tenantID, other := uuid.New(), uuid.New()
	created, err := r.UpsertTerminal(ctx, newTerminal(tenantID, uuid.New(), "AV-"+uuid.NewString()))
	require.NoError(t, err)

	got, err := r.GetTerminal(ctx, tenantID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.TerminalSerial, got.TerminalSerial)

	_, err = r.GetTerminal(ctx, tenantID, uuid.New())
	assert.ErrorIs(t, err, repo.ErrTerminalNotFound)

	// RLS: another tenant must not observe the row, even with the exact id.
	_, err = r.GetTerminal(ctx, other, created.ID)
	assert.ErrorIs(t, err, repo.ErrTerminalNotFound)
}

func TestFiscalAdminRepo_UpdateTerminal_PartialPatch(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewFiscalAdminRepo(sharedPool)

	tenantID := uuid.New()
	created, err := r.UpsertTerminal(ctx, newTerminal(tenantID, uuid.New(), "AV-"+uuid.NewString()))
	require.NoError(t, err)

	label := "Bar Kasası"
	updated, err := r.UpdateTerminal(ctx, tenantID, created.ID, repo.TerminalPatch{Label: &label})
	require.NoError(t, err)
	assert.Equal(t, label, updated.Label)
	assert.Equal(t, created.BasketMode, updated.BasketMode, "an absent field must survive the patch")
	assert.Equal(t, created.IsActive, updated.IsActive)

	// An empty label is a value, not an absence: it clears the field.
	empty := ""
	cleared, err := r.UpdateTerminal(ctx, tenantID, created.ID, repo.TerminalPatch{Label: &empty})
	require.NoError(t, err)
	assert.Equal(t, "", cleared.Label)

	_, err = r.UpdateTerminal(ctx, tenantID, uuid.New(), repo.TerminalPatch{Label: &label})
	assert.ErrorIs(t, err, repo.ErrTerminalNotFound)
}

func TestFiscalAdminRepo_ListTerminals_ScopedToBranchAndOrdered(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewFiscalAdminRepo(sharedPool)

	tenantID, branchID, otherBranch := uuid.New(), uuid.New(), uuid.New()

	first, err := r.UpsertTerminal(ctx, newTerminal(tenantID, branchID, "AV-a-"+uuid.NewString()))
	require.NoError(t, err)
	second, err := r.UpsertTerminal(ctx, newTerminal(tenantID, branchID, "AV-b-"+uuid.NewString()))
	require.NoError(t, err)
	_, err = r.UpsertTerminal(ctx, newTerminal(tenantID, otherBranch, "AV-c-"+uuid.NewString()))
	require.NoError(t, err)

	list, err := r.ListTerminals(ctx, tenantID, branchID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	// Oldest first: the same order FiscalTerminalDirectory.Resolve uses to pick
	// the serving terminal, so the admin list's head is the one a sale targets.
	assert.Equal(t, first.ID, list[0].ID)
	assert.Equal(t, second.ID, list[1].ID)
}

// TestFiscalAdminRepo_ReplaceSections_FullSync proves the sync is a true mirror
// of the device: added, changed and removed sections all take effect.
func TestFiscalAdminRepo_ReplaceSections_FullSync(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewFiscalAdminRepo(sharedPool)

	tenantID := uuid.New()
	terminal, err := r.UpsertTerminal(ctx, newTerminal(tenantID, uuid.New(), "AV-"+uuid.NewString()))
	require.NoError(t, err)

	initial := []domain.DeviceSection{
		{SectionNo: 1, Name: "KDV %1", TaxPermyriad: 100},
		{SectionNo: 2, Name: "KDV %10", TaxPermyriad: 1000},
		{SectionNo: 9, Name: "Eski Kısım", TaxPermyriad: 800},
	}
	require.NoError(t, r.ReplaceSections(ctx, tenantID, terminal.ID, initial))

	stored, err := r.ListSections(ctx, tenantID, terminal.ID)
	require.NoError(t, err)
	require.Len(t, stored, 3)
	assert.Equal(t, 1, stored[0].SectionNo, "sections come back ordered by number")
	assert.False(t, stored[0].SyncedAt.IsZero())

	// The device now reports: section 2's tax changed, 3 is new, 9 is gone.
	next := []domain.DeviceSection{
		{SectionNo: 1, Name: "KDV %1", TaxPermyriad: 100},
		{SectionNo: 2, Name: "KDV %20", TaxPermyriad: 2000},
		{SectionNo: 3, Name: "KDV %10", TaxPermyriad: 1000},
	}
	require.NoError(t, r.ReplaceSections(ctx, tenantID, terminal.ID, next))

	stored, err = r.ListSections(ctx, tenantID, terminal.ID)
	require.NoError(t, err)
	require.Len(t, stored, 3)
	assert.Equal(t, 2000, stored[1].TaxPermyriad, "a device-side tax change must be picked up")
	assert.Equal(t, "KDV %20", stored[1].Name)
	assert.Equal(t, 3, stored[2].SectionNo)
	for _, s := range stored {
		assert.NotEqual(t, 9, s.SectionNo, "a section removed on the device must disappear")
	}
}

func TestFiscalAdminRepo_ReplaceSections_TenantIsolation(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewFiscalAdminRepo(sharedPool)

	tenantID, other := uuid.New(), uuid.New()
	terminal, err := r.UpsertTerminal(ctx, newTerminal(tenantID, uuid.New(), "AV-"+uuid.NewString()))
	require.NoError(t, err)

	require.NoError(t, r.ReplaceSections(ctx, tenantID, terminal.ID,
		[]domain.DeviceSection{{SectionNo: 1, Name: "KDV %1", TaxPermyriad: 100}}))

	// RLS scopes fiscal_device_sections by tenant_id, so another tenant sees
	// nothing — and, crucially, its own "full sync" cannot delete these rows.
	leaked, err := r.ListSections(ctx, other, terminal.ID)
	require.NoError(t, err)
	assert.Empty(t, leaked)

	require.NoError(t, r.ReplaceSections(ctx, other, terminal.ID, nil))

	survived, err := r.ListSections(ctx, tenantID, terminal.ID)
	require.NoError(t, err)
	assert.Len(t, survived, 1, "another tenant's sync must not clear our sections")
}

// TestFiscalAdminRepo_ReplaceSectionMappings_FullReplace exercises the DELETE
// path added by migration payment/000005.
//
// Caveat: this test cannot prove the GRANT is present. The TestMain bootstrap
// issues ALTER DEFAULT PRIVILEGES ... GRANT DELETE ON TABLES TO app_runtime, so
// app_runtime holds DELETE here regardless of the migration. The grant is
// asserted by inspection against migrations/payment/000005 — in production only
// that file gives app_runtime the privilege.
func TestFiscalAdminRepo_ReplaceSectionMappings_FullReplace(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewFiscalAdminRepo(sharedPool)

	tenantID, branchID := uuid.New(), uuid.New()
	catA, catB, catC := uuid.New(), uuid.New(), uuid.New()

	require.NoError(t, r.ReplaceSectionMappings(ctx, tenantID, branchID, []repo.FiscalSectionMapping{
		{CategoryID: catA, SectionNo: 1},
		{CategoryID: catB, SectionNo: 2},
	}))

	mappings, err := r.ListSectionMappings(ctx, tenantID, branchID)
	require.NoError(t, err)
	require.Len(t, mappings, 2)

	// Replacing with a different set drops catB and adds catC; re-mapping catA
	// to another section must not trip the (tenant, branch, category) unique
	// index, which a naive insert-only "sync" would.
	require.NoError(t, r.ReplaceSectionMappings(ctx, tenantID, branchID, []repo.FiscalSectionMapping{
		{CategoryID: catA, SectionNo: 3},
		{CategoryID: catC, SectionNo: 1},
	}))

	mappings, err = r.ListSectionMappings(ctx, tenantID, branchID)
	require.NoError(t, err)
	require.Len(t, mappings, 2)
	got := map[uuid.UUID]int{}
	for _, m := range mappings {
		got[m.CategoryID] = m.SectionNo
	}
	assert.Equal(t, 3, got[catA])
	assert.Equal(t, 1, got[catC])
	assert.NotContains(t, got, catB, "a removed mapping must be deleted, not left behind")

	// An empty set is a legitimate "unmap everything".
	require.NoError(t, r.ReplaceSectionMappings(ctx, tenantID, branchID, nil))
	mappings, err = r.ListSectionMappings(ctx, tenantID, branchID)
	require.NoError(t, err)
	assert.Empty(t, mappings)
}

func TestFiscalAdminRepo_ReplaceSectionMappings_ScopedToBranch(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewFiscalAdminRepo(sharedPool)

	tenantID, branchOne, branchTwo := uuid.New(), uuid.New(), uuid.New()
	catA := uuid.New()

	require.NoError(t, r.ReplaceSectionMappings(ctx, tenantID, branchOne,
		[]repo.FiscalSectionMapping{{CategoryID: catA, SectionNo: 1}}))
	require.NoError(t, r.ReplaceSectionMappings(ctx, tenantID, branchTwo,
		[]repo.FiscalSectionMapping{{CategoryID: catA, SectionNo: 2}}))

	// Replacing branchTwo must leave branchOne untouched.
	require.NoError(t, r.ReplaceSectionMappings(ctx, tenantID, branchTwo, nil))

	one, err := r.ListSectionMappings(ctx, tenantID, branchOne)
	require.NoError(t, err)
	require.Len(t, one, 1)
	assert.Equal(t, 1, one[0].SectionNo)
}

// TestFiscalAdminRepo_FeedsSaleTimeSectionResolve closes the loop: what the
// admin API writes is exactly what the sale path reads. A terminal is paired,
// its sections synced and a category mapped; FiscalSectionDirectory.Resolve
// must then return that section's number with the DEVICE's tax rate.
func TestFiscalAdminRepo_FeedsSaleTimeSectionResolve(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	admin := repo.NewFiscalAdminRepo(sharedPool)
	sections := repo.NewFiscalSectionDirectory(sharedPool)
	terminals := repo.NewFiscalTerminalDirectory(sharedPool)

	tenantID, branchID, categoryID := uuid.New(), uuid.New(), uuid.New()

	terminal, err := admin.UpsertTerminal(ctx, newTerminal(tenantID, branchID, "AV-"+uuid.NewString()))
	require.NoError(t, err)

	require.NoError(t, admin.ReplaceSections(ctx, tenantID, terminal.ID, []domain.DeviceSection{
		{SectionNo: 1, Name: "KDV %1", TaxPermyriad: 100},
		{SectionNo: 4, Name: "KDV %20", TaxPermyriad: 2000},
	}))
	require.NoError(t, admin.ReplaceSectionMappings(ctx, tenantID, branchID,
		[]repo.FiscalSectionMapping{{CategoryID: categoryID, SectionNo: 4}}))

	ref, err := terminals.Resolve(ctx, tenantID, branchID)
	require.NoError(t, err)
	assert.Equal(t, terminal.TerminalSerial, ref.Serial)
	assert.Equal(t, "B1", ref.VendorBranchRef)

	sectionNo, taxPermyriad, err := sections.Resolve(ctx, tenantID, branchID, categoryID)
	require.NoError(t, err)
	assert.Equal(t, 4, sectionNo)
	assert.Equal(t, 2000, taxPermyriad, "the tax rate must come from the device sync, not the mapping")

	// An unmapped category is refused rather than defaulted — a guessed section
	// would print a wrong VAT rate on a legal receipt.
	_, _, err = sections.Resolve(ctx, tenantID, branchID, uuid.New())
	assert.ErrorIs(t, err, repo.ErrNoSectionMapping)
}
