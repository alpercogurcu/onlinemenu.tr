package domain

import (
	"time"

	"github.com/google/uuid"
)

// SalesSummaryFilter narrows ReportRepo.SalesSummary to one branch and a
// half-open [From, To) window, evaluated against checks.closed_at.
type SalesSummaryFilter struct {
	BranchID uuid.UUID
	From, To time.Time
	// TZ is an IANA zone name (e.g. "Europe/Istanbul"), validated by the
	// service before reaching the repo. It only affects ByDay's date bucketing
	// (closed_at AT TIME ZONE TZ), not the [From, To) window itself, which is
	// always evaluated in UTC (closed_at is stored as timestamptz).
	TZ string
}

// TaxLine is one KDV-rate bucket of a sales summary: Gross is the raw
// item total at that rate, Base and Tax split it per the tax-inclusive
// formula NewTaxLine implements.
type TaxLine struct {
	RateBPS     int
	Gross, Base int64
	Tax         int64
}

// NewTaxLine splits a tax-inclusive gross amount into base and tax at the
// given basis-point rate, using integer arithmetic throughout (money is
// int64 kuruş — no floating point). The rounding half-up on the division
// remainder (`+ den/2` before the final `/ den`) matches the pilot's Global
// Constraints tax formula verbatim: tax = (gross*bps + (10000+bps)/2) /
// (10000+bps), base = gross - tax. Lives in domain (not repo) so it is
// exercised by a Docker-free unit test.
func NewTaxLine(rateBPS int, gross int64) TaxLine {
	den := int64(10000 + rateBPS)
	tax := (gross*int64(rateBPS) + den/2) / den
	return TaxLine{RateBPS: rateBPS, Gross: gross, Base: gross - tax, Tax: tax}
}

// DayLine is one calendar-day bucket of a sales summary, keyed by
// closed_at's local date in the filter's TZ.
type DayLine struct {
	Date       string // "YYYY-MM-DD" in SalesSummaryFilter.TZ
	Gross      int64
	CheckCount int64
}

// SourceLine is one checks.source bucket ("pos" | "online_qr") of a sales
// summary.
type SourceLine struct {
	Source     string
	CheckCount int64
	Gross      int64
}

// SalesSummary is the sales report for one branch and window: check counts,
// gross/cancelled amounts, item count, and three independent breakdowns
// (tax rate, day, source). All *Line slices are non-nil (empty, not nil) even
// when a window has no data — see ReportRepo.SalesSummary's doc comment.
type SalesSummary struct {
	ClosedCheckCount    int64
	CancelledCheckCount int64
	// GrossSales is the sum of active order items (see InactiveOrderStatuses)
	// belonging to checks CLOSED in the window.
	GrossSales int64
	// CancelledAmount is the same sum for checks CANCELLED in the window —
	// what would have been billed had the check not been cancelled.
	CancelledAmount int64
	// ItemCount is sum(quantity) of active items on closed checks.
	ItemCount int64
	ByTaxRate []TaxLine
	ByDay     []DayLine
	BySource  []SourceLine
}
