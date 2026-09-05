package repo

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// ReportRepo answers sales-report aggregate queries against
// checks/orders/order_items. Unlike CheckRepo/OrderRepo it has no per-row
// CRUD surface: every method backs pos/service's sales-summary use case
// (consumed by GET /pos/reports/sale-details).
type ReportRepo struct{}

func NewReportRepo() *ReportRepo { return &ReportRepo{} }

// excludedOrderStatuses renders domain.InactiveOrderStatuses as the []string
// every SalesSummary query needs for its `o.status <> ALL($n::text[])` join
// predicate — array parameters travel as []string, not []domain.OrderStatus,
// for the same simple-protocol-encoding reason as uuidStrings (order_repo.go).
func excludedOrderStatuses() []string {
	out := make([]string, len(domain.InactiveOrderStatuses))
	for i, s := range domain.InactiveOrderStatuses {
		out[i] = string(s)
	}
	return out
}

// SalesSummary aggregates checks/orders/order_items for one branch and
// window into check counts, gross/cancelled amounts, item count, and three
// breakdowns (tax rate, day, source). It runs four independent queries in
// the caller's transaction rather than one mega-query: each breakdown groups
// by a different key, and a single query GROUPing by all of them at once
// would need a window-function reshuffle that is harder to read for what is,
// at pilot scale, four cheap sequential scans of the same small row set.
//
// Every query joins orders with `o.status <> ALL(excluded)` to drop
// rejected/cancelled order items from the money figures — the same exclusion
// CheckRepo.GetTotal/TotalsByCheckIDs apply, fed from the same
// domain.InactiveOrderStatuses source of truth — and filters on
// c.closed_at, which CheckRepo.UpdateStatus sets for BOTH 'closed' and
// 'cancelled' targets (see that method's SQL: `closed_at = CASE WHEN $2 IN
// ('closed','cancelled') THEN NOW() ...`), so a cancelled check's closed_at
// is never NULL and never needs a COALESCE fallback to updated_at.
//
// tenant scoping relies on RLS alone (no explicit c.tenant_id predicate):
// this mirrors CheckRepo/OrderRepo in this module (GetByID, List, ...), none
// of which repeat tenant_id in the WHERE clause, rather than payment/repo's
// convention of an explicit tenant_id column filter — domain.SalesSummaryFilter
// carries no TenantID field to filter on.
//
// ByTaxRate/ByDay/BySource are always non-nil (empty slice, not nil) even
// for an empty window, so JSON serializes them as `[]` rather than `null`.
func (r *ReportRepo) SalesSummary(ctx context.Context, tx pgx.Tx, f domain.SalesSummaryFilter) (domain.SalesSummary, error) {
	excluded := excludedOrderStatuses()

	summary, err := r.statusTotals(ctx, tx, f, excluded)
	if err != nil {
		return domain.SalesSummary{}, err
	}

	summary.ByTaxRate, err = r.byTaxRate(ctx, tx, f, excluded)
	if err != nil {
		return domain.SalesSummary{}, err
	}

	summary.ByDay, err = r.byDay(ctx, tx, f, excluded)
	if err != nil {
		return domain.SalesSummary{}, err
	}

	summary.BySource, err = r.bySource(ctx, tx, f, excluded)
	if err != nil {
		return domain.SalesSummary{}, err
	}

	return summary, nil
}

// statusTotals fills the scalar counters (ClosedCheckCount, GrossSales,
// ItemCount, CancelledCheckCount, CancelledAmount) from a single GROUP BY
// c.status query: 'closed' and 'cancelled' are the only two statuses a check
// in the window can have here (open checks have no closed_at, so the
// closed_at window predicate already excludes them).
func (r *ReportRepo) statusTotals(ctx context.Context, tx pgx.Tx, f domain.SalesSummaryFilter, excluded []string) (domain.SalesSummary, error) {
	const q = `
		SELECT c.status, COUNT(DISTINCT c.id),
		       COALESCE(SUM(oi.quantity * oi.unit_price_amount), 0),
		       COALESCE(SUM(oi.quantity), 0)
		FROM checks c
		LEFT JOIN orders o ON o.check_id = c.id AND o.status <> ALL($4::text[])
		LEFT JOIN order_items oi ON oi.order_id = o.id
		WHERE c.branch_id = $1
		  AND c.status IN ('closed', 'cancelled')
		  AND c.closed_at >= $2 AND c.closed_at < $3
		GROUP BY c.status`

	rows, err := tx.Query(ctx, q, f.BranchID, f.From, f.To, excluded)
	if err != nil {
		return domain.SalesSummary{}, fmt.Errorf("pos/repo/report: status totals: %w", err)
	}
	defer rows.Close()

	var summary domain.SalesSummary
	for rows.Next() {
		var status string
		var count, amount, qty int64
		if err := rows.Scan(&status, &count, &amount, &qty); err != nil {
			return domain.SalesSummary{}, fmt.Errorf("pos/repo/report: status totals scan: %w", err)
		}
		switch domain.CheckStatus(status) {
		case domain.CheckStatusClosed:
			summary.ClosedCheckCount = count
			summary.GrossSales = amount
			summary.ItemCount = qty
		case domain.CheckStatusCancelled:
			summary.CancelledCheckCount = count
			summary.CancelledAmount = amount
		}
	}
	if err := rows.Err(); err != nil {
		return domain.SalesSummary{}, fmt.Errorf("pos/repo/report: status totals: %w", err)
	}
	return summary, nil
}

// byTaxRate breaks GrossSales down per oi.tax_rate_bps, closed checks only.
// This joins order_items with INNER (not LEFT, unlike statusTotals/byDay/
// bySource): a closed check with no active items has no tax rate to bucket
// under, and a LEFT JOIN would produce a spurious {RateBPS: 0, Gross: 0}
// group from its NULL tax_rate_bps row.
func (r *ReportRepo) byTaxRate(ctx context.Context, tx pgx.Tx, f domain.SalesSummaryFilter, excluded []string) ([]domain.TaxLine, error) {
	const q = `
		SELECT oi.tax_rate_bps, COALESCE(SUM(oi.quantity * oi.unit_price_amount), 0)
		FROM checks c
		JOIN orders o ON o.check_id = c.id AND o.status <> ALL($4::text[])
		JOIN order_items oi ON oi.order_id = o.id
		WHERE c.branch_id = $1
		  AND c.status = 'closed'
		  AND c.closed_at >= $2 AND c.closed_at < $3
		GROUP BY oi.tax_rate_bps
		ORDER BY oi.tax_rate_bps`

	rows, err := tx.Query(ctx, q, f.BranchID, f.From, f.To, excluded)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/report: by tax rate: %w", err)
	}
	defer rows.Close()

	lines := make([]domain.TaxLine, 0)
	for rows.Next() {
		var bps int
		var gross int64
		if err := rows.Scan(&bps, &gross); err != nil {
			return nil, fmt.Errorf("pos/repo/report: by tax rate scan: %w", err)
		}
		lines = append(lines, taxLine(bps, gross))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pos/repo/report: by tax rate: %w", err)
	}
	return lines, nil
}

// taxLine splits a tax-inclusive gross amount into base and tax at the given
// basis-point rate, using integer arithmetic throughout (money is int64
// kuruş — no floating point). The rounding half-up on the division remainder
// (`+ den/2` before the final `/ den`) matches the pilot's Global
// Constraints tax formula verbatim: tax = (gross*bps + (10000+bps)/2) /
// (10000+bps), base = gross - tax.
func taxLine(bps int, gross int64) domain.TaxLine {
	den := int64(10000 + bps)
	tax := (gross*int64(bps) + den/2) / den
	return domain.TaxLine{RateBPS: bps, Gross: gross, Base: gross - tax, Tax: tax}
}

// byDay breaks down closed-check counts and gross sales per calendar day,
// bucketed by closed_at's local date in f.TZ (an IANA zone name validated by
// the service before it reaches here). LEFT JOIN (unlike byTaxRate): a
// closed check with no active items must still count toward that day's
// CheckCount, contributing 0 to Gross.
func (r *ReportRepo) byDay(ctx context.Context, tx pgx.Tx, f domain.SalesSummaryFilter, excluded []string) ([]domain.DayLine, error) {
	const q = `
		SELECT to_char((c.closed_at AT TIME ZONE $4)::date, 'YYYY-MM-DD'),
		       COUNT(DISTINCT c.id),
		       COALESCE(SUM(oi.quantity * oi.unit_price_amount), 0)
		FROM checks c
		LEFT JOIN orders o ON o.check_id = c.id AND o.status <> ALL($5::text[])
		LEFT JOIN order_items oi ON oi.order_id = o.id
		WHERE c.branch_id = $1
		  AND c.status = 'closed'
		  AND c.closed_at >= $2 AND c.closed_at < $3
		GROUP BY 1
		ORDER BY 1`

	rows, err := tx.Query(ctx, q, f.BranchID, f.From, f.To, f.TZ, excluded)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/report: by day: %w", err)
	}
	defer rows.Close()

	lines := make([]domain.DayLine, 0)
	for rows.Next() {
		var line domain.DayLine
		if err := rows.Scan(&line.Date, &line.CheckCount, &line.Gross); err != nil {
			return nil, fmt.Errorf("pos/repo/report: by day scan: %w", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pos/repo/report: by day: %w", err)
	}
	return lines, nil
}

// bySource breaks down closed-check counts and gross sales per
// checks.source ('pos' | 'online_qr'). LEFT JOIN for the same reason as
// byDay: a closed check with no active items still counts under its source.
func (r *ReportRepo) bySource(ctx context.Context, tx pgx.Tx, f domain.SalesSummaryFilter, excluded []string) ([]domain.SourceLine, error) {
	const q = `
		SELECT c.source, COUNT(DISTINCT c.id), COALESCE(SUM(oi.quantity * oi.unit_price_amount), 0)
		FROM checks c
		LEFT JOIN orders o ON o.check_id = c.id AND o.status <> ALL($4::text[])
		LEFT JOIN order_items oi ON oi.order_id = o.id
		WHERE c.branch_id = $1
		  AND c.status = 'closed'
		  AND c.closed_at >= $2 AND c.closed_at < $3
		GROUP BY c.source
		ORDER BY c.source`

	rows, err := tx.Query(ctx, q, f.BranchID, f.From, f.To, excluded)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/report: by source: %w", err)
	}
	defer rows.Close()

	lines := make([]domain.SourceLine, 0)
	for rows.Next() {
		var line domain.SourceLine
		if err := rows.Scan(&line.Source, &line.CheckCount, &line.Gross); err != nil {
			return nil, fmt.Errorf("pos/repo/report: by source scan: %w", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pos/repo/report: by source: %w", err)
	}
	return lines, nil
}
