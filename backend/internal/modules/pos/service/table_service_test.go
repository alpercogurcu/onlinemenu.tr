package service_test

// Table plan integration tests (Sprint-5 Wave 1). These run against the
// shared testcontainers pool from integration_test.go's TestMain, exactly
// like the check/order integration tests in this package.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/modules/pos/service"
)

// tenantB is a second tenant, distinct from tenantA, used only by the
// cross-tenant RLS-visibility test below.
var tenantB = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")

// newOpenTestTable creates a zone + one "empty" table under tenantA/branchA
// (or the given branch), for use as CheckService.Open's table_id target.
func newOpenTestTable(t *testing.T, ctx context.Context, branchID uuid.UUID) domain.Table {
	t.Helper()
	tableRepo := repo.NewTableRepo()

	var zone domain.TableZone
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		zone, err = tableRepo.CreateZone(ctx, tx, domain.TableZone{
			TenantID: tenantA, BranchID: branchID, Name: "Zone", IsActive: true,
		})
		return err
	})
	require.NoError(t, err)

	var tbl domain.Table
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		tbl, err = tableRepo.CreateTable(ctx, tx, domain.Table{
			TenantID: tenantA, BranchID: branchID, ZoneID: zone.ID, Name: "Masa", Capacity: 4, IsActive: true,
		})
		return err
	})
	require.NoError(t, err)
	return tbl
}

// ---------------------------------------------------------------------------
// CheckService.Open <-> table lifecycle
// ---------------------------------------------------------------------------

func TestCheckService_Open_WithTable_OccupiesTableAndDerivesLabel(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	tbl := newOpenTestTable(t, ctx, branchA)

	c, err := svc.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:   branchA,
		TableID:    &tbl.ID,
		TableLabel: "ignored client value",
		OpenedBy:   staffA,
	})
	require.NoError(t, err)
	assert.Equal(t, tbl.ID, *c.TableID)
	assert.Equal(t, tbl.Name, c.TableLabel, "table_label must be derived from the table's name, not the client-supplied value")

	tableRepo := repo.NewTableRepo()
	var reloaded domain.Table
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		reloaded, err = tableRepo.GetTableByID(ctx, tx, tbl.ID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.TableStatusOccupied, reloaded.Status)
}

func TestCheckService_Open_TableAlreadyOccupied_Returns409(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	tbl := newOpenTestTable(t, ctx, branchA)

	_, err := svc.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID: branchA, TableID: &tbl.ID, OpenedBy: staffA,
	})
	require.NoError(t, err)

	_, err = svc.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID: branchA, TableID: &tbl.ID, OpenedBy: staffA,
	})
	assert.ErrorIs(t, err, pub.ErrTableOccupied)
}

func TestCheckService_Open_TableBranchMismatch(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	tableInBranchA := newOpenTestTable(t, ctx, branchA)

	_, err := svc.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID: branchB, TableID: &tableInBranchA.ID, OpenedBy: staffA,
	})
	assert.ErrorIs(t, err, pub.ErrTableBranchMismatch)
}

// TestCheckService_Open_ConcurrentSameTable_OneSucceeds proves the table row
// lock (TableRepo.GetTableForUpdate), not the unique index alone, is what
// makes exactly one of N concurrent Open calls against the same table
// succeed — mirroring TestCheckService_ConcurrentClose_EmitsExactlyOneEvent's
// rationale for Close.
func TestCheckService_Open_ConcurrentSameTable_OneSucceeds(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	tbl := newOpenTestTable(t, ctx, branchA)

	const n = 6
	var successCount atomic.Int32
	var occupiedCount atomic.Int32
	var otherErrCount atomic.Int32

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, err := svc.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
				BranchID: branchA, TableID: &tbl.ID, OpenedBy: staffA,
			})
			switch {
			case err == nil:
				successCount.Add(1)
			case errors.Is(err, pub.ErrTableOccupied):
				occupiedCount.Add(1)
			default:
				otherErrCount.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(0), otherErrCount.Load(), "no unexpected errors")
	assert.Equal(t, int32(1), successCount.Load(), "exactly one Open call must succeed for the same table")
	assert.Equal(t, int32(n-1), occupiedCount.Load(), "all other calls must observe the table already occupied")
}

func TestCheckService_Close_ReleasesTableToCleaning(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	tbl := newOpenTestTable(t, ctx, branchA)

	c, err := svc.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID: branchA, TableID: &tbl.ID, OpenedBy: staffA,
	})
	require.NoError(t, err)

	_, err = svc.Close(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, staffA)
	require.NoError(t, err)

	tableRepo := repo.NewTableRepo()
	var reloaded domain.Table
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		reloaded, err = tableRepo.GetTableByID(ctx, tx, tbl.ID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.TableStatusCleaning, reloaded.Status)
}

func TestCheckService_Cancel_ReleasesTableToCleaning(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	tbl := newOpenTestTable(t, ctx, branchA)

	c, err := svc.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID: branchA, TableID: &tbl.ID, OpenedBy: staffA,
	})
	require.NoError(t, err)

	_, err = svc.Cancel(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, staffA)
	require.NoError(t, err)

	tableRepo := repo.NewTableRepo()
	var reloaded domain.Table
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		reloaded, err = tableRepo.GetTableByID(ctx, tx, tbl.ID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.TableStatusCleaning, reloaded.Status)
}

// TestCheckService_Close_TableManuallyReset_StillSucceeds is the "close takes
// priority over the derived table state" regression test: if staff already
// manually reset the table to "empty" (TableService.SetStatus) before the
// check is closed, Close must still succeed — releaseTableToCleaning's guard
// tolerates the 0-rows-affected outcome rather than failing the whole close.
func TestCheckService_Close_TableManuallyReset_StillSucceeds(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	tbl := newOpenTestTable(t, ctx, branchA)

	c, err := svc.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID: branchA, TableID: &tbl.ID, OpenedBy: staffA,
	})
	require.NoError(t, err)

	// Manager resets the table back to empty out of band (e.g. correcting a
	// mistaken open) while the check is still open.
	tableRepo := repo.NewTableRepo()
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := tableRepo.UpdateStatus(ctx, tx, tbl.ID, domain.TableStatusCleaning, domain.TableStatusOccupied)
		if err != nil {
			return err
		}
		_, err = tableRepo.UpdateStatus(ctx, tx, tbl.ID, domain.TableStatusEmpty, domain.TableStatusCleaning)
		return err
	})
	require.NoError(t, err)

	_, err = svc.Close(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, staffA)
	assert.NoError(t, err, "closing the check must succeed even though the table is no longer 'occupied'")
}

// ---------------------------------------------------------------------------
// TableService.SetStatus
// ---------------------------------------------------------------------------

func TestTableService_SetStatus_ManualOccupyForbidden(t *testing.T) {
	ctx := context.Background()
	tableSvc := newTableService()
	tbl := newOpenTestTable(t, ctx, branchA)

	_, err := tableSvc.SetStatus(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), tbl.ID, domain.TableStatusOccupied)
	assert.ErrorIs(t, err, service.ErrManualOccupyForbidden)
}

func TestTableService_SetStatus_ValidManualTransitions(t *testing.T) {
	ctx := context.Background()
	tableSvc := newTableService()
	tbl := newOpenTestTable(t, ctx, branchA)

	reserved, err := tableSvc.SetStatus(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), tbl.ID, domain.TableStatusReserved)
	require.NoError(t, err)
	assert.Equal(t, domain.TableStatusReserved, reserved.Status)

	emptied, err := tableSvc.SetStatus(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), tbl.ID, domain.TableStatusEmpty)
	require.NoError(t, err)
	assert.Equal(t, domain.TableStatusEmpty, emptied.Status)
}

func TestTableService_SetStatus_InvalidTransitionRejected(t *testing.T) {
	ctx := context.Background()
	tableSvc := newTableService()
	tbl := newOpenTestTable(t, ctx, branchA) // starts "empty"

	// empty -> cleaning is allowed by the machine, but cleaning -> reserved is not.
	_, err := tableSvc.SetStatus(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), tbl.ID, domain.TableStatusCleaning)
	require.NoError(t, err)

	_, err = tableSvc.SetStatus(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), tbl.ID, domain.TableStatusReserved)
	assert.ErrorIs(t, err, pub.ErrInvalidTransition)
}

// ---------------------------------------------------------------------------
// Branch-scope / cross-tenant authorization
// ---------------------------------------------------------------------------

func TestTableAuthz_CreateZone_ForeignBranchForbidden(t *testing.T) {
	ctx := context.Background()
	tableSvc := newTableService()

	_, err := tableSvc.CreateZone(ctx, tenantA, branchPrincipal(branchB), domain.TableZone{
		BranchID: branchA, Name: "Zone",
	})
	assert.ErrorIs(t, err, pub.ErrBranchForbidden)
}

func TestTableAuthz_CreateTable_ForeignBranchForbidden(t *testing.T) {
	ctx := context.Background()
	tableSvc := newTableService()

	_, err := tableSvc.CreateTable(ctx, tenantA, branchPrincipal(branchB), domain.Table{
		BranchID: branchA, ZoneID: uuid.New(), Name: "Masa",
	})
	assert.ErrorIs(t, err, pub.ErrBranchForbidden)
}

func TestTableAuthz_SetStatus_ForeignBranchForbidden(t *testing.T) {
	ctx := context.Background()
	tableSvc := newTableService()
	tbl := newOpenTestTable(t, ctx, branchA)

	_, err := tableSvc.SetStatus(ctx, tenantA, branchPrincipal(branchB), tbl.ID, domain.TableStatusReserved)
	assert.ErrorIs(t, err, pub.ErrBranchForbidden)
}

func TestTableAuthz_CrossTenant_NotFound(t *testing.T) {
	ctx := context.Background()
	tableSvc := newTableService()
	tbl := newOpenTestTable(t, ctx, branchA)

	// tenantB's own chain-wide principal cannot even see tenantA's table row
	// (RLS, layer 1) — the service surfaces this as pub.ErrNotFound, never a
	// 403, since the row is invisible rather than merely branch-forbidden.
	principalB := chainWidePrincipal()
	principalB.TenantID = tenantB

	_, err := tableSvc.SetStatus(ctx, tenantB, principalB, tbl.ID, domain.TableStatusReserved)
	assert.ErrorIs(t, err, pub.ErrNotFound)
}

// ---------------------------------------------------------------------------
// ListTables: zone-grouped, single-request floor plan
// ---------------------------------------------------------------------------

// TestTableService_ListTables_OrderedByZoneThenTable proves ListTables
// returns every table already annotated with its zone's name/floor and
// ordered by (zone floor, zone name, table name), so the HTTP layer's
// toZonePlanResponse can group these rows into zone-labeled sections without
// a second request to GET /zones and without re-sorting.
// Uses a dedicated branch id (not the shared branchA) so the exact-count
// assertion below isn't polluted by tables other tests in this package
// create under branchA against the same shared testcontainers database.
func TestTableService_ListTables_OrderedByZoneThenTable(t *testing.T) {
	ctx := context.Background()
	tableSvc := newTableService()
	tableRepo := repo.NewTableRepo()
	branch := uuid.New()

	var zoneGround, zoneFirst domain.TableZone
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		zoneGround, err = tableRepo.CreateZone(ctx, tx, domain.TableZone{
			TenantID: tenantA, BranchID: branch, Name: "Zemin Kat", Floor: 0, IsActive: true,
		})
		if err != nil {
			return err
		}
		zoneFirst, err = tableRepo.CreateZone(ctx, tx, domain.TableZone{
			TenantID: tenantA, BranchID: branch, Name: "Birinci Kat", Floor: 1, IsActive: true,
		})
		return err
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		for _, tc := range []struct {
			zoneID uuid.UUID
			name   string
		}{
			{zoneFirst.ID, "Masa F1"},
			{zoneGround.ID, "Masa G2"},
			{zoneGround.ID, "Masa G1"},
		} {
			if _, err := tableRepo.CreateTable(ctx, tx, domain.Table{
				TenantID: tenantA, BranchID: branch, ZoneID: tc.zoneID, Name: tc.name, IsActive: true,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)

	entries, err := tableSvc.ListTables(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), branch)
	require.NoError(t, err)
	require.Len(t, entries, 3)

	// Ground floor (0) sorts before first floor (1); within ground floor,
	// table name order applies.
	assert.Equal(t, "Zemin Kat", entries[0].ZoneName)
	assert.Equal(t, "Masa G1", entries[0].Table.Name)
	assert.Equal(t, "Zemin Kat", entries[1].ZoneName)
	assert.Equal(t, "Masa G2", entries[1].Table.Name)
	assert.Equal(t, "Birinci Kat", entries[2].ZoneName)
	assert.Equal(t, "Masa F1", entries[2].Table.Name)
}

// TestTableService_ListTables_SameFloorAndName_KeepsZonesContiguous is the
// regression test for the grouping defect the advisor caught: two zones
// sharing the same (floor, name) — a legal, realistic case (e.g. two
// branches each naming their ground-floor zone "Bahçe") — must NOT have
// their tables interleave under (floor, name, table.name) sort, or
// http.toZonePlanResponse's contiguity-based grouping would split one zone
// into two non-adjacent sections. TableRepo.ListTablesByBranch's ORDER BY
// includes z.id as a tiebreaker specifically to prevent this.
func TestTableService_ListTables_SameFloorAndName_KeepsZonesContiguous(t *testing.T) {
	ctx := context.Background()
	tableSvc := newTableService()
	tableRepo := repo.NewTableRepo()
	branch := uuid.New()

	var zoneA, zoneB domain.TableZone
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		zoneA, err = tableRepo.CreateZone(ctx, tx, domain.TableZone{
			TenantID: tenantA, BranchID: branch, Name: "Bahçe", Floor: 0, IsActive: true,
		})
		if err != nil {
			return err
		}
		zoneB, err = tableRepo.CreateZone(ctx, tx, domain.TableZone{
			TenantID: tenantA, BranchID: branch, Name: "Bahçe", Floor: 0, IsActive: true,
		})
		return err
	})
	require.NoError(t, err)

	// Table names deliberately interleave ACROSS zones in lexical order
	// ("Masa 1" < "Masa 2" < "Masa 3" < "Masa 4", alternating zoneA/zoneB):
	// without the z.id tiebreaker, sorting by (floor, zone.name, table.name)
	// alone sorts purely by table name here, producing A,B,A,B — which is
	// exactly the non-contiguous ordering this test must catch. (Names like
	// "A1","A2","B1","B2" would NOT discriminate: they'd already sort
	// contiguous by table name alone, A,A,B,B, passing even without the fix.)
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		for _, tc := range []struct {
			zoneID uuid.UUID
			name   string
		}{
			{zoneA.ID, "Masa 1"},
			{zoneB.ID, "Masa 2"},
			{zoneA.ID, "Masa 3"},
			{zoneB.ID, "Masa 4"},
		} {
			if _, err := tableRepo.CreateTable(ctx, tx, domain.Table{
				TenantID: tenantA, BranchID: branch, ZoneID: tc.zoneID, Name: tc.name, IsActive: true,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)

	entries, err := tableSvc.ListTables(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), branch)
	require.NoError(t, err)
	require.Len(t, entries, 4)

	// Assert every same-zone_id row is contiguous: once we've moved past a
	// zone_id, it must never reappear.
	seen := map[uuid.UUID]bool{}
	for i, e := range entries {
		if i > 0 && entries[i-1].Table.ZoneID != e.Table.ZoneID {
			assert.Falsef(t, seen[e.Table.ZoneID],
				"zone %s reappeared after another zone's rows — grouping would split it into two sections", e.Table.ZoneID)
		}
		seen[e.Table.ZoneID] = true
	}
}

// ---------------------------------------------------------------------------
// UpdateZone / UpdateTable: PATCH must not zero omitted fields
// ---------------------------------------------------------------------------

// TestTableService_UpdateZone_PartialPatch_LeavesOmittedFieldsUnchanged is
// the regression test for the "PATCH silently deactivates" defect: a patch
// that only sets Name must leave Floor/IsActive exactly as they were.
func TestTableService_UpdateZone_PartialPatch_LeavesOmittedFieldsUnchanged(t *testing.T) {
	tableSvc := newTableService()

	created, err := tableSvc.CreateZone(chainWideCtx(t, context.Background()), tenantA, chainWidePrincipal(), domain.TableZone{
		BranchID: branchA, Name: "Zemin Kat", Floor: 2,
	})
	require.NoError(t, err)
	require.True(t, created.IsActive)

	newName := "Zemin Kat (Yenilendi)"
	updated, err := tableSvc.UpdateZone(chainWideCtx(t, context.Background()), tenantA, chainWidePrincipal(), created.ID, service.ZonePatch{
		Name: &newName,
	})
	require.NoError(t, err)
	assert.Equal(t, newName, updated.Name)
	assert.Equal(t, 2, updated.Floor, "omitted floor must be left unchanged, not zeroed")
	assert.True(t, updated.IsActive, "omitted is_active must be left unchanged, not deactivated")
}

// TestTableService_UpdateTable_PartialPatch_LeavesOmittedFieldsUnchanged
// mirrors the zone test above for tables: patching only Capacity must not
// zero Name/IsActive/ZoneID.
func TestTableService_UpdateTable_PartialPatch_LeavesOmittedFieldsUnchanged(t *testing.T) {
	ctx := context.Background()
	tableSvc := newTableService()
	tbl := newOpenTestTable(t, ctx, branchA)

	newCapacity := 8
	updated, err := tableSvc.UpdateTable(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), tbl.ID, service.TablePatch{
		Capacity: &newCapacity,
	})
	require.NoError(t, err)
	assert.Equal(t, 8, updated.Capacity)
	assert.Equal(t, tbl.Name, updated.Name, "omitted name must be left unchanged")
	assert.Equal(t, tbl.ZoneID, updated.ZoneID, "omitted zone_id must be left unchanged")
	assert.True(t, updated.IsActive, "omitted is_active must be left unchanged, not deactivated")
}
