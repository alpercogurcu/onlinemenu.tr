package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/platform/auth"
)

// settledWindow is how far back the poll reports terminal outcomes. It must
// comfortably exceed the client's polling interval (a few seconds) so no
// station can miss a transition between two polls, while staying short enough
// that the response does not grow into a shift-long history.
const settledWindow = 5 * time.Minute

// FiscalBranchStatus is the branch-wide view of fiscal registration state.
// AsOf is the single server clock the client compares its own against;
// PendingAges are derived from it so the client never has to trust its own.
type FiscalBranchStatus struct {
	BranchID        uuid.UUID
	AsOf            time.Time
	Pending         []FiscalPendingItem
	RecentlySettled []FiscalSettledItem
}

// FiscalPendingItem is one in-flight registration with its server-computed age.
type FiscalPendingItem struct {
	PaymentID    uuid.UUID
	CheckID      *uuid.UUID
	AmountTotal  int64
	RegisteredAt time.Time
	AgeSeconds   int64
}

// FiscalSettledItem is one registration that reached a terminal state inside
// the recency window. These mirror the repo rows rather than re-exporting
// them, so the HTTP layer depends on the service contract only and a repo
// column change cannot silently alter the wire shape.
//
// AmountTotal is carried here as well as on the pending item: the client keeps
// deducting a settled payment's amount for as long as it stays in the window,
// so a payment that just left Pending does not read as collectable again.
type FiscalSettledItem struct {
	PaymentID     uuid.UUID
	CheckID       *uuid.UUID
	AmountTotal   int64
	Status        string
	FailureReason *string
	SettledAt     time.Time
}

// FiscalBranchStatusFor answers the multi-station poll: which fiscal
// registrations are in flight anywhere in this branch, and which just settled.
//
// A branch runs several POS stations against the same backend; a sale started
// on station 1 is invisible to station 2, which would otherwise treat the
// check as unpaid (or, worse, optimistically settled) and take the money
// twice. RecentlySettled exists so a station that sees a payment leave
// Pending can tell completion from failure without holding
// payment.payment.read.
func (s *PaymentService) FiscalBranchStatusFor(
	ctx context.Context,
	principal auth.Principal,
	branchID uuid.UUID,
) (FiscalBranchStatus, error) {
	if branchID == uuid.Nil {
		return FiscalBranchStatus{}, fmt.Errorf("payment/service: branch_id is required")
	}
	if err := requireBranch(ctx, principal, branchID); err != nil {
		return FiscalBranchStatus{}, err
	}

	// One clock for the whole response — the API host's. as_of, every
	// age_seconds and the settled-window edge all derive from this single
	// reading; SQL never evaluates now() for this endpoint, so a station never
	// sees timestamps that disagree because the DB host drifted.
	asOf := time.Now().UTC()
	settledCutoff := asOf.Add(-settledWindow)

	status := FiscalBranchStatus{
		BranchID:        branchID,
		AsOf:            asOf,
		Pending:         []FiscalPendingItem{},
		RecentlySettled: []FiscalSettledItem{},
	}

	err := s.db.WithTenantReadTx(ctx, principal.TenantID, func(tx pgx.Tx) error {
		pending, err := s.statusRepo.ListPendingByBranch(ctx, tx, principal.TenantID, branchID)
		if err != nil {
			return err
		}
		for _, row := range pending {
			age := asOf.Sub(row.RegisteredAt)
			if age < 0 {
				age = 0 // clock skew between the insert and this read must not surface as a negative age
			}
			status.Pending = append(status.Pending, FiscalPendingItem{
				PaymentID:    row.PaymentID,
				CheckID:      row.CheckID,
				AmountTotal:  row.AmountTotal,
				RegisteredAt: row.RegisteredAt,
				AgeSeconds:   int64(age.Seconds()),
			})
		}

		settled, err := s.statusRepo.ListRecentlySettledByBranch(ctx, tx, principal.TenantID, branchID, settledCutoff)
		if err != nil {
			return err
		}
		for _, row := range settled {
			status.RecentlySettled = append(status.RecentlySettled, FiscalSettledItem{
				PaymentID:     row.PaymentID,
				CheckID:       row.CheckID,
				AmountTotal:   row.AmountTotal,
				Status:        row.Status,
				FailureReason: row.FailureReason,
				SettledAt:     row.SettledAt,
			})
		}
		return nil
	})
	if err != nil {
		return FiscalBranchStatus{}, fmt.Errorf("payment/service: fiscal branch status: %w", err)
	}
	return status, nil
}
