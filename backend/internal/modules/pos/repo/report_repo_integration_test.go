package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
	"onlinemenu.tr/internal/modules/pos/repo"
)

// TestReportRepo_SalesSummary seeds three checks under a dedicated branch
// (isolation from every other test in this package, which all reuse branchA):
//
//   - A: closed, source pos. One active order with two lines (2×10000 @1000bps,
//     1×5000 @2000bps) plus one CANCELLED order (1×99999) that must never
//     contribute to any figure — the same exclusion rule GetTotal enforces.
//   - B: closed, source online_qr. One active order (1×3000 @1000bps).
//   - C: cancelled. One active order (1×4000 @1000bps) — must land in
//     CancelledAmount/CancelledCheckCount, not GrossSales/ClosedCheckCount.
//
// A and B close on two different calendar days (in TZ) inside the window, so
// ByDay must produce two rows; C's own day is irrelevant to ByDay since only
// 'closed' checks feed that breakdown.
func TestReportRepo_SalesSummary(t *testing.T) {
	ctx := context.Background()
	checkRepo := repo.NewCheckRepo()
	orderRepo := repo.NewOrderRepo()
	reportRepo := repo.NewReportRepo()

	branch := uuid.New()
	const tz = "Europe/Istanbul" // fixed UTC+3, no DST since 2016 — no boundary surprises here

	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	dayA := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC) // -> 2026-06-03 in Europe/Istanbul
	dayB := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC) // -> 2026-06-05
	dayC := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)

	openCheck := func(tx pgx.Tx, source domain.Source) domain.Check {
		c, err := checkRepo.Create(ctx, tx, domain.Check{
			TenantID: tenantA, BranchID: branch, TableLabel: "Masa Rapor",
			Status: domain.CheckStatusOpen, OpenedBy: &staffA, Source: source,
		})
		require.NoError(t, err)
		return c
	}

	addOrder := func(tx pgx.Tx, checkID uuid.UUID, status domain.OrderStatus, items ...domain.OrderItem) {
		_, err := orderRepo.Create(ctx, tx, domain.Order{
			TenantID: tenantA, BranchID: branch, CheckID: &checkID,
			OrderChannel: domain.OrderChannelDineIn, Status: status, Items: items,
		})
		require.NoError(t, err)
	}

	item := func(bps int, unitPrice int64, qty int) domain.OrderItem {
		return domain.OrderItem{
			ProductID: uuid.New(), ProductName: "Test Item",
			ProductPriceAmount: unitPrice, ProductCurrency: "TRY",
			TaxRateBPS: bps, Quantity: qty, UnitPriceAmount: unitPrice,
		}
	}

	backdateClosedAt := func(tx pgx.Tx, checkID uuid.UUID, closedAt time.Time) {
		_, err := tx.Exec(ctx, `UPDATE checks SET closed_at=$1 WHERE id=$2`, closedAt, checkID)
		require.NoError(t, err)
	}

	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		a := openCheck(tx, domain.SourcePOS)
		addOrder(tx, a.ID, domain.OrderStatusPending, item(1000, 10000, 2), item(2000, 5000, 1))
		addOrder(tx, a.ID, domain.OrderStatusCancelled, item(1000, 99999, 1))
		_, err := checkRepo.UpdateStatus(ctx, tx, a.ID, domain.CheckStatusClosed, domain.CheckStatusOpen, &staffA)
		require.NoError(t, err)
		backdateClosedAt(tx, a.ID, dayA)

		b := openCheck(tx, domain.SourceOnlineQR)
		addOrder(tx, b.ID, domain.OrderStatusPending, item(1000, 3000, 1))
		_, err = checkRepo.UpdateStatus(ctx, tx, b.ID, domain.CheckStatusClosed, domain.CheckStatusOpen, &staffA)
		require.NoError(t, err)
		backdateClosedAt(tx, b.ID, dayB)

		c := openCheck(tx, domain.SourcePOS)
		addOrder(tx, c.ID, domain.OrderStatusPending, item(1000, 4000, 1))
		_, err = checkRepo.UpdateStatus(ctx, tx, c.ID, domain.CheckStatusCancelled, domain.CheckStatusOpen, &staffA)
		require.NoError(t, err)
		backdateClosedAt(tx, c.ID, dayC)

		return nil
	})
	require.NoError(t, err)

	var summary domain.SalesSummary
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		summary, err = reportRepo.SalesSummary(ctx, tx, domain.SalesSummaryFilter{
			BranchID: branch, From: from, To: to, TZ: tz,
		})
		return err
	})
	require.NoError(t, err)

	assert.Equal(t, int64(2), summary.ClosedCheckCount)
	assert.Equal(t, int64(1), summary.CancelledCheckCount)
	assert.Equal(t, int64(28000), summary.GrossSales)
	assert.Equal(t, int64(4000), summary.CancelledAmount)
	assert.Equal(t, int64(4), summary.ItemCount)

	require.Equal(t, []domain.TaxLine{
		{RateBPS: 1000, Gross: 23000, Base: 20909, Tax: 2091},
		{RateBPS: 2000, Gross: 5000, Base: 4167, Tax: 833},
	}, summary.ByTaxRate)

	require.Equal(t, []domain.DayLine{
		{Date: "2026-06-03", Gross: 25000, CheckCount: 1},
		{Date: "2026-06-05", Gross: 3000, CheckCount: 1},
	}, summary.ByDay)

	require.Equal(t, []domain.SourceLine{
		{Source: "online_qr", Gross: 3000, CheckCount: 1},
		{Source: "pos", Gross: 25000, CheckCount: 1},
	}, summary.BySource)
}

// TestReportRepo_SalesSummary_RLSIsolation guards the one predicate
// SalesSummary does NOT write explicitly: tenant scoping. It seeds two
// tenants' checks under the SAME branch id (a collision that could only
// happen for two different tenants, but is exactly the case RLS — not the
// query text — must prevent from mixing), and asserts tenantA's summary
// reflects only its own check.
func TestReportRepo_SalesSummary_RLSIsolation(t *testing.T) {
	ctx := context.Background()
	checkRepo := repo.NewCheckRepo()
	orderRepo := repo.NewOrderRepo()
	reportRepo := repo.NewReportRepo()

	branch := uuid.New() // deliberately shared across tenantA/tenantB below
	from := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC)
	closedAt := time.Date(2027, 1, 5, 10, 0, 0, 0, time.UTC)

	seed := func(tenantID uuid.UUID, gross int64) {
		err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
			c, err := checkRepo.Create(ctx, tx, domain.Check{
				TenantID: tenantID, BranchID: branch, TableLabel: "RLS Masa",
				Status: domain.CheckStatusOpen, OpenedBy: &staffA,
			})
			if err != nil {
				return err
			}
			if _, err := orderRepo.Create(ctx, tx, domain.Order{
				TenantID: tenantID, BranchID: branch, CheckID: &c.ID,
				OrderChannel: domain.OrderChannelDineIn, Status: domain.OrderStatusPending,
				Items: []domain.OrderItem{{
					ProductID: uuid.New(), ProductName: "RLS Item",
					ProductPriceAmount: gross, ProductCurrency: "TRY",
					TaxRateBPS: 1000, Quantity: 1, UnitPriceAmount: gross,
				}},
			}); err != nil {
				return err
			}
			if _, err := checkRepo.UpdateStatus(ctx, tx, c.ID, domain.CheckStatusClosed, domain.CheckStatusOpen, &staffA); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE checks SET closed_at=$1 WHERE id=$2`, closedAt, c.ID)
			return err
		})
		require.NoError(t, err)
	}

	seed(tenantA, 7000)
	seed(tenantB, 9000)

	var summary domain.SalesSummary
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		summary, err = reportRepo.SalesSummary(ctx, tx, domain.SalesSummaryFilter{
			BranchID: branch, From: from, To: to, TZ: "Europe/Istanbul",
		})
		return err
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), summary.ClosedCheckCount, "tenantB's check on the same branch id must not be counted")
	assert.Equal(t, int64(7000), summary.GrossSales, "tenantB's gross must not leak into tenantA's summary")
}

// TestReportRepo_SalesSummary_EmptyWindow_ReturnsEmptySlicesNotNil guards the
// "[] not null" JSON contract: a branch/window with no data must still come
// back with non-nil, empty breakdown slices.
func TestReportRepo_SalesSummary_EmptyWindow_ReturnsEmptySlicesNotNil(t *testing.T) {
	ctx := context.Background()
	reportRepo := repo.NewReportRepo()

	var summary domain.SalesSummary
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		summary, err = reportRepo.SalesSummary(ctx, tx, domain.SalesSummaryFilter{
			BranchID: uuid.New(),
			From:     time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
			To:       time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC),
			TZ:       "Europe/Istanbul",
		})
		return err
	})
	require.NoError(t, err)

	assert.Zero(t, summary.ClosedCheckCount)
	assert.Zero(t, summary.GrossSales)
	assert.NotNil(t, summary.ByTaxRate)
	assert.Empty(t, summary.ByTaxRate)
	assert.NotNil(t, summary.ByDay)
	assert.Empty(t, summary.ByDay)
	assert.NotNil(t, summary.BySource)
	assert.Empty(t, summary.BySource)
}
