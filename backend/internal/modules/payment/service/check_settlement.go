package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/platform/auth"
)

// CheckSettlement is the authoritative money-taken view of a single check.
type CheckSettlement struct {
	CheckID      uuid.UUID
	AsOf         time.Time
	Completed    []CheckSettledPayment
	PendingTotal int64
}

// CheckSettledPayment is one collected payment: id and amount only.
//
// The id is the point. An aggregate ("2500 already collected") cannot be
// reconciled against the station's own optimistic list — it cannot tell whether
// the sum includes the payment it just took locally or a different one of equal
// value, which is exactly how a split bill gets charged twice. With the id the
// station dedupes exactly.
type CheckSettledPayment struct {
	PaymentID   uuid.UUID
	AmountTotal int64
}

// CheckSettlementFor answers "how much of this check is actually paid" for a
// principal that cannot read payments.
//
// The gap this closes: a cashier holds payment.fiscal_status.read but not
// payment.payment.read, so completed payments are invisible to them. The POS
// bridged that with the fiscal poll's 5-minute recently_settled window; once a
// payment aged out of that window the station's balance snapped back to the
// full amount and the next cashier collected it again. This endpoint has no
// window — it reports the check's settled state at any age.
//
// Branch defense is a filter, not a rejection. Unlike the fiscal poll, the
// client supplies no branch_id to validate: check_id is the only input, and
// answering "forbidden" for another branch's check would confirm that the id
// exists. A cross-branch probe therefore reads as an unpaid check (empty
// completed, zero pending) — indistinguishable from an id that does not exist.
func (s *PaymentService) CheckSettlementFor(
	ctx context.Context,
	principal auth.Principal,
	checkID uuid.UUID,
) (CheckSettlement, error) {
	if checkID == uuid.Nil {
		return CheckSettlement{}, fmt.Errorf("payment/service: check_id is required")
	}

	// One clock for the whole response — the API host's, matching the fiscal
	// poll. A station compares as_of across both endpoints to order what it
	// learns; if one came from the DB host and the other from the API host,
	// host drift would make a stale reading look newer than a fresh one.
	asOf := time.Now().UTC()

	settlement := CheckSettlement{
		CheckID:   checkID,
		AsOf:      asOf,
		Completed: []CheckSettledPayment{},
	}

	branchID := branchScopeFilter(ctx, principal)

	err := s.db.WithTenantReadTx(ctx, principal.TenantID, func(tx pgx.Tx) error {
		completed, err := s.paymentRepo.ListCompletedByCheck(ctx, tx, principal.TenantID, checkID, branchID)
		if err != nil {
			return err
		}
		for _, row := range completed {
			settlement.Completed = append(settlement.Completed, CheckSettledPayment{
				PaymentID:   row.PaymentID,
				AmountTotal: row.AmountTotal,
			})
		}

		total, err := s.paymentRepo.PendingTotalForCheckInBranch(ctx, tx, principal.TenantID, checkID, branchID)
		if err != nil {
			return err
		}
		settlement.PendingTotal = total
		return nil
	})
	if err != nil {
		return CheckSettlement{}, fmt.Errorf("payment/service: check settlement: %w", err)
	}
	return settlement, nil
}

// branchScopeFilter resolves ADR-AUTH-001 layer 3 scope into a SQL predicate
// value: nil for tenant scope (manager — every branch), otherwise the
// principal's own branch.
//
// Scope comes from the OPA decision in ctx rather than from inspecting RoleIDs,
// so role-to-scope mapping stays in authz.rego and cannot drift here — the same
// reasoning as requireBranch, which this complements rather than replaces
// (requireBranch rejects a client-supplied branch_id; there is none here).
//
// A branch-scoped principal whose BranchID is uuid.Nil filters to uuid.Nil,
// which matches no row (payments.branch_id is NOT NULL). That is deliberate:
// auth.Principal.HasBranchAccess reads nil as "every branch", which is correct
// for a chain owner but unsafe for a mis-provisioned chain-wide cashier, and
// memberships.branch_id is nullable with nothing tying branch-scoped system
// roles to a non-null branch. Failing closed to an empty result is the safe
// direction: it shows an unpaid check, and the cashier declines to close rather
// than collecting money already taken.
func branchScopeFilter(ctx context.Context, principal auth.Principal) *uuid.UUID {
	if scope, ok := auth.ScopeFromContext(ctx); ok && scope == "tenant" {
		return nil
	}
	branchID := principal.BranchID
	return &branchID
}
