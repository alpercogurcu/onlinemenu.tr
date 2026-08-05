package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/modules/pos/service"
)

// Guest (QR) placement writes rows no staff path can produce — a check with
// opened_by NULL and opened_by_kind 'guest_qr' — and the DB enforces that
// pairing with a CHECK constraint. Only a real database can tell whether the
// service and the constraint agree.

func newGuestOrderService() *service.OrderService {
	return service.NewOrderService(service.OrderParams{
		DB:        sharedPool,
		OrderRepo: repo.NewOrderRepo(),
		CheckRepo: repo.NewCheckRepo(),
		TableRepo: repo.NewTableRepo(),
		Logger:    zap.NewNop(),
	})
}

// seedGuestTable creates a zone + table for branchA and returns the table.
func seedGuestTable(t *testing.T, name string) domain.Table {
	t.Helper()
	ctx := context.Background()
	tableRepo := repo.NewTableRepo()

	var table domain.Table
	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		zone, err := tableRepo.CreateZone(ctx, tx, domain.TableZone{
			TenantID: tenantA, BranchID: branchA, Name: "QR " + name, IsActive: true,
		})
		if err != nil {
			return err
		}
		table, err = tableRepo.CreateTable(ctx, tx, domain.Table{
			TenantID: tenantA, BranchID: branchA, ZoneID: zone.ID,
			Name: name, Capacity: 4, IsActive: true,
		})
		return err
	}))
	return table
}

func guestRequestFor(table domain.Table) pub.GuestOrderRequest {
	return pub.GuestOrderRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		TableID:        table.ID,
		QRCodeID:       uuid.New(),
		GuestSessionID: uuid.New(),
		Lines: []pub.GuestOrderLine{{
			ProductID:       uuid.New(),
			Name:            "Latte",
			BasePriceAmount: 4500,
			UnitPriceAmount: 5000,
			Currency:        "TRY",
			TaxRateBPS:      1000,
			Quantity:        2,
		}},
	}
}

func TestOrderService_PlaceGuest_OpensGuestCheckOnEmptyTable(t *testing.T) {
	ctx := context.Background()
	table := seedGuestTable(t, "Masa QR-1")
	orders := newGuestOrderService()

	result, err := orders.PlaceGuest(ctx, guestRequestFor(table), nil)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, result.CheckID)
	assert.Equal(t, "pending", result.Status)

	check, err := newCheckService().GetByID(ctx, tenantA, result.CheckID)
	require.NoError(t, err)
	assert.Nil(t, check.OpenedBy, "an anonymous diner has no person row — the sentinel UUID is banned")
	assert.Equal(t, domain.OpenedByKindGuestQR, check.OpenedByKind)
	assert.Equal(t, domain.SourceOnlineQR, check.Source)
	assert.Equal(t, "Masa QR-1", check.TableLabel, "the label comes from the table row, not the client")

	order, err := orders.GetByID(ctx, tenantA, result.OrderID)
	require.NoError(t, err)
	assert.Equal(t, domain.SourceOnlineQR, order.Source)
	assert.Equal(t, domain.OrderChannelDineIn, order.OrderChannel,
		"channel and source are orthogonal: a QR order is dine_in AND online_qr")
	assert.Equal(t, domain.OrderStatusPending, order.Status)
	require.Len(t, order.Items, 1)
	assert.Equal(t, int64(5000), order.Items[0].UnitPriceAmount)

	// The table is now occupied, exactly as a staff-opened check would leave it.
	var status string
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status FROM tables WHERE id = $1`, table.ID).Scan(&status)
	}))
	assert.Equal(t, string(domain.TableStatusOccupied), status)
}

// TestOrderService_PlaceGuest_JoinsExistingStaffCheck: the ADR is explicit
// that a QR order joins whatever check the table already has — a diner
// ordering a second round must not open a second adisyon the cashier then has
// to reconcile.
func TestOrderService_PlaceGuest_JoinsExistingStaffCheck(t *testing.T) {
	ctx := context.Background()
	table := seedGuestTable(t, "Masa QR-2")
	checks := newCheckService()
	orders := newGuestOrderService()

	staffCheck, err := checks.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:     branchA,
		TableID:      &table.ID,
		OpenedBy:     &staffA,
		OpenedByKind: domain.OpenedByKindStaff,
	})
	require.NoError(t, err)

	result, err := orders.PlaceGuest(ctx, guestRequestFor(table), nil)
	require.NoError(t, err)
	assert.Equal(t, staffCheck.ID, result.CheckID)

	second, err := orders.PlaceGuest(ctx, guestRequestFor(table), nil)
	require.NoError(t, err)
	assert.Equal(t, staffCheck.ID, second.CheckID, "a second round joins the same check too")
}

// TestOrderService_PlaceGuest_CleaningTable_ReturnsNotReady is plan note D4:
// "masa dolu" is the wrong thing to tell someone at an empty, uncleared table.
func TestOrderService_PlaceGuest_CleaningTable_ReturnsNotReady(t *testing.T) {
	ctx := context.Background()
	table := seedGuestTable(t, "Masa QR-3")
	orders := newGuestOrderService()

	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := repo.NewTableRepo().UpdateStatus(ctx, tx, table.ID,
			domain.TableStatusCleaning, domain.TableStatusEmpty)
		return err
	}))

	_, err := orders.PlaceGuest(ctx, guestRequestFor(table), nil)
	require.ErrorIs(t, err, pub.ErrTableNotReady)
	require.NotErrorIs(t, err, pub.ErrTableOccupied,
		"the two conditions must stay distinguishable — the diner's next step differs")
}

// TestCheckService_Open_CleaningTable_StillOccupied pins that the D4 carve-out
// is guest-only: the staff contract did not change when openCheckTx was
// extracted.
func TestCheckService_Open_CleaningTable_StillOccupied(t *testing.T) {
	ctx := context.Background()
	table := seedGuestTable(t, "Masa QR-4")
	checks := newCheckService()

	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := repo.NewTableRepo().UpdateStatus(ctx, tx, table.ID,
			domain.TableStatusCleaning, domain.TableStatusEmpty)
		return err
	}))

	_, err := checks.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:     branchA,
		TableID:      &table.ID,
		OpenedBy:     &staffA,
		OpenedByKind: domain.OpenedByKindStaff,
	})
	require.ErrorIs(t, err, pub.ErrTableOccupied)
	require.NotErrorIs(t, err, pub.ErrTableNotReady)
}

func TestOrderService_PlaceGuest_WrongBranch_IsRejected(t *testing.T) {
	ctx := context.Background()
	table := seedGuestTable(t, "Masa QR-5")
	orders := newGuestOrderService()

	req := guestRequestFor(table)
	req.BranchID = uuid.New() // a QR sticker that outlived a table move

	_, err := orders.PlaceGuest(ctx, req, nil)
	require.ErrorIs(t, err, pub.ErrTableBranchMismatch)
}

func TestOrderService_PlaceGuest_UnknownTable_IsNotFound(t *testing.T) {
	ctx := context.Background()
	orders := newGuestOrderService()

	req := guestRequestFor(domain.Table{ID: uuid.New()})
	_, err := orders.PlaceGuest(ctx, req, nil)
	require.ErrorIs(t, err, pub.ErrTableNotFound)
}

// TestOrderService_PlaceGuest_LinkerFailureRollsBackTheOrder is the whole
// reason the binding is a callback: if the storefront cannot record "this
// session placed this order", the order must not exist either — otherwise the
// kitchen cooks something the diner can neither see nor be billed for
// predictably, and their retry orders it twice.
func TestOrderService_PlaceGuest_LinkerFailureRollsBackTheOrder(t *testing.T) {
	ctx := context.Background()
	table := seedGuestTable(t, "Masa QR-6")
	orders := newGuestOrderService()

	boom := assert.AnError
	_, err := orders.PlaceGuest(ctx, guestRequestFor(table), func(context.Context, pgx.Tx, pub.GuestOrderResult) error {
		return boom
	})
	require.ErrorIs(t, err, boom)

	var orderCount, checkCount int
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM checks WHERE table_id = $1`, table.ID).Scan(&checkCount); err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM orders WHERE branch_id = $1 AND source = 'online_qr'
			 AND check_id IN (SELECT id FROM checks WHERE table_id = $2)`,
			branchA, table.ID).Scan(&orderCount)
	}))
	assert.Zero(t, checkCount, "the guest check must be rolled back with the order")
	assert.Zero(t, orderCount)

	var status string
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status FROM tables WHERE id = $1`, table.ID).Scan(&status)
	}))
	assert.Equal(t, string(domain.TableStatusEmpty), status,
		"the table must not be left occupied by a check that no longer exists")
}

// TestOrderService_PlaceGuest_ConcurrentPlacementsShareOneCheck: two diners at
// the same table submitting at once must land on one adisyon, not two.
func TestOrderService_PlaceGuest_ConcurrentPlacementsShareOneCheck(t *testing.T) {
	ctx := context.Background()
	table := seedGuestTable(t, "Masa QR-7")
	orders := newGuestOrderService()

	type outcome struct {
		res pub.GuestOrderResult
		err error
	}
	results := make(chan outcome, 2)
	for range 2 {
		go func() {
			res, err := orders.PlaceGuest(ctx, guestRequestFor(table), nil)
			results <- outcome{res, err}
		}()
	}

	first, second := <-results, <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	assert.Equal(t, first.res.CheckID, second.res.CheckID,
		"the check row lock must serialize concurrent guest placements onto one check")
	assert.NotEqual(t, first.res.OrderID, second.res.OrderID)
}
