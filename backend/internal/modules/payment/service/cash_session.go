package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// CashSessionService orchestrates ADR-DATA-008's branch cash session
// reconciliation: opening/closing the drawer, in-shift movements, and the
// close guard.
type CashSessionService struct {
	db         *db.Pool
	sessions   *repo.CashSessionRepo
	fiscalRepo *repo.FiscalStatusRepo
	logger     *zap.Logger
}

// CashSessionParams groups fx-injected dependencies.
type CashSessionParams struct {
	fx.In

	DB         *db.Pool
	Sessions   *repo.CashSessionRepo
	FiscalRepo *repo.FiscalStatusRepo
	Logger     *zap.Logger
}

func NewCashSessionService(p CashSessionParams) *CashSessionService {
	return &CashSessionService{db: p.DB, sessions: p.Sessions, fiscalRepo: p.FiscalRepo, logger: p.Logger}
}

// CashSessionCannotCloseError mirrors Odoo's _cannot_close_session: it
// collects every blocking reason instead of failing on the first, so the
// caller (and eventually the POS UI) can show the cashier everything standing
// between them and closing the drawer, not just the first check that failed.
type CashSessionCannotCloseError struct {
	Reasons []string
}

func (e *CashSessionCannotCloseError) Error() string {
	return fmt.Sprintf("payment/service: cannot close cash session: %s", strings.Join(e.Reasons, "; "))
}

// OpenCashSessionRequest carries the inputs for opening a branch's drawer.
type OpenCashSessionRequest struct {
	BranchID             uuid.UUID
	OpeningCountedAmount int64
	OpeningNotes         string
}

// Open starts a new cash session for a branch. ADR-DATA-008 Karar 1: at most
// one open session per branch, enforced by cash_sessions_one_open_per_branch —
// this call surfaces that as pub.ErrCashSessionAlreadyOpen rather than a raw
// constraint-violation error.
//
// It returns a CashSessionView, same as GetActive/SubmitClosingCount/Close:
// every caller-facing method returns the full computed view rather than the
// bare persisted struct, so a handler can never accidentally report stale or
// fabricated reconciliation figures (see buildView's callers below — this was
// a real bug during development: an earlier version of the HTTP layer reused
// a "zero movements" shortcut for Open's response on the submit/close
// responses too, silently reporting movements_net=0 after movements had
// already been recorded).
func (s *CashSessionService) Open(ctx context.Context, principal auth.Principal, req OpenCashSessionRequest) (CashSessionView, error) {
	if req.BranchID == uuid.Nil {
		return CashSessionView{}, fmt.Errorf("payment/service: branch_id is required")
	}
	if err := requireBranch(ctx, principal, req.BranchID); err != nil {
		return CashSessionView{}, err
	}
	if req.OpeningCountedAmount < 0 {
		return CashSessionView{}, fmt.Errorf("payment/service: opening_counted_amount must not be negative")
	}

	var view CashSessionView
	err := s.db.WithTenantTx(ctx, principal.TenantID, func(tx pgx.Tx) error {
		session, err := s.sessions.Open(ctx, tx, domain.CashSession{
			TenantID:             principal.TenantID,
			BranchID:             req.BranchID,
			OpeningCountedAmount: req.OpeningCountedAmount,
			OpeningNotes:         req.OpeningNotes,
			OpenedBy:             principal.PersonID,
		})
		if err != nil {
			return err
		}
		view, err = s.buildView(ctx, tx, session)
		return err
	})
	if errors.Is(err, repo.ErrCashSessionAlreadyOpen) {
		return CashSessionView{}, pub.ErrCashSessionAlreadyOpen
	}
	if err != nil {
		return CashSessionView{}, fmt.Errorf("payment/service: open cash session: %w", err)
	}
	return view, nil
}

// CashSessionView is the read model for a session: the persisted aggregate
// plus the values ADR-DATA-008 requires to be computed fresh on every read
// rather than stored (see the migration's column comments for why).
type CashSessionView struct {
	Session domain.CashSession

	// MovementsNet is the net (in - out) of cash_movements for this session.
	MovementsNet int64
	// CashPaymentsTaken is completed cash payments in the session's window
	// (see CashSessionRepo.SumCompletedCashPayments for the window rationale).
	CashPaymentsTaken int64
	// ExpectedClose = opening counted amount + cash payments taken + movements.
	ExpectedClose int64
	// Difference = closing counted amount - expected close. nil until a
	// closing count has been submitted — there is nothing to compare yet.
	Difference *int64
}

// GetActive returns the branch's current open session (opened or
// closing_control) with its live reconciliation figures.
func (s *CashSessionService) GetActive(ctx context.Context, principal auth.Principal, branchID uuid.UUID) (CashSessionView, error) {
	if branchID == uuid.Nil {
		return CashSessionView{}, fmt.Errorf("payment/service: branch_id is required")
	}
	if err := requireBranch(ctx, principal, branchID); err != nil {
		return CashSessionView{}, err
	}

	var view CashSessionView
	err := s.db.WithTenantReadTx(ctx, principal.TenantID, func(tx pgx.Tx) error {
		session, err := s.sessions.GetActiveByBranch(ctx, tx, principal.TenantID, branchID)
		if err != nil {
			return err
		}
		view, err = s.buildView(ctx, tx, session)
		return err
	})
	if errors.Is(err, repo.ErrNotFound) {
		return CashSessionView{}, pub.ErrNotFound
	}
	if err != nil {
		return CashSessionView{}, fmt.Errorf("payment/service: get active cash session: %w", err)
	}
	return view, nil
}

// buildView computes the live reconciliation figures for session within tx.
// A closed session's window has a fixed upper bound (ClosedAt); an open one is
// unbounded so the figures move in real time as new cash sales/movements
// land — matching the Odoo note that a session's reported balance formula
// changes at close so the report stops moving afterwards.
func (s *CashSessionService) buildView(ctx context.Context, tx pgx.Tx, session domain.CashSession) (CashSessionView, error) {
	cashTaken, err := s.sessions.SumCompletedCashPayments(ctx, tx, session.TenantID, session.BranchID, session.OpenedAt, session.ClosedAt)
	if err != nil {
		return CashSessionView{}, err
	}
	movementsNet, err := s.sessions.SumMovementsNet(ctx, tx, session.TenantID, session.ID)
	if err != nil {
		return CashSessionView{}, err
	}

	view := CashSessionView{
		Session:           session,
		MovementsNet:      movementsNet,
		CashPaymentsTaken: cashTaken,
		ExpectedClose:     session.OpeningCountedAmount + cashTaken + movementsNet,
	}
	if session.ClosingCountedAmount != nil {
		diff := *session.ClosingCountedAmount - view.ExpectedClose
		view.Difference = &diff
	}
	return view, nil
}

// RecordMovementRequest carries the inputs for one in-shift cash in/out.
type RecordMovementRequest struct {
	Direction   domain.CashMovementDirection
	AmountMinor int64
	Reason      string
}

// RecordMovement records a cash in/out against an open session. Movements are
// only accepted while the session is 'opened' — once a closing count has been
// submitted (closing_control) the drawer is being reconciled against a
// specific counted figure, and a movement landing after that would silently
// invalidate it.
func (s *CashSessionService) RecordMovement(ctx context.Context, principal auth.Principal, sessionID uuid.UUID, req RecordMovementRequest) (domain.CashMovement, error) {
	if sessionID == uuid.Nil {
		return domain.CashMovement{}, fmt.Errorf("payment/service: session id is required")
	}
	if !req.Direction.Valid() {
		return domain.CashMovement{}, fmt.Errorf("payment/service: invalid movement direction %q", req.Direction)
	}
	if req.AmountMinor <= 0 {
		return domain.CashMovement{}, fmt.Errorf("payment/service: amount_minor must be positive")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return domain.CashMovement{}, fmt.Errorf("payment/service: reason is required")
	}

	var movement domain.CashMovement
	err := s.db.WithTenantTx(ctx, principal.TenantID, func(tx pgx.Tx) error {
		session, err := s.sessions.GetByIDForUpdate(ctx, tx, principal.TenantID, sessionID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, session.BranchID); err != nil {
			return err
		}
		if session.Status != domain.CashSessionOpened {
			return fmt.Errorf("payment/service: cash session is %q, movements can only be recorded while opened: %w",
				session.Status, domain.ErrInvalidCashSessionTransition)
		}
		movement, err = s.sessions.InsertMovement(ctx, tx, domain.CashMovement{
			TenantID:    principal.TenantID,
			BranchID:    session.BranchID,
			SessionID:   sessionID,
			Direction:   req.Direction,
			AmountMinor: req.AmountMinor,
			Reason:      req.Reason,
			CreatedBy:   principal.PersonID,
		})
		return err
	})
	if errors.Is(err, repo.ErrNotFound) {
		return domain.CashMovement{}, pub.ErrNotFound
	}
	if err != nil {
		return domain.CashMovement{}, fmt.Errorf("payment/service: record cash movement: %w", err)
	}
	return movement, nil
}

// SubmitClosingCountRequest carries the inputs for the first step of closing.
type SubmitClosingCountRequest struct {
	ClosingCountedAmount int64
	Denominations        []domain.DenominationCount
	Notes                string
}

// SubmitClosingCount is the first of the two-step close (ADR-DATA-008): it
// records what the cashier counted and moves the session to closing_control.
// It does not close anything by itself — Close does that, after the guard.
// Calling this again while already in closing_control is a deliberate
// "recount" path (domain.allowedTransitions' closing_control self-loop), not
// an error — a cashier who mis-typed a count is expected to resubmit before
// the manager closes.
func (s *CashSessionService) SubmitClosingCount(ctx context.Context, principal auth.Principal, sessionID uuid.UUID, req SubmitClosingCountRequest) (CashSessionView, error) {
	if sessionID == uuid.Nil {
		return CashSessionView{}, fmt.Errorf("payment/service: session id is required")
	}
	if req.ClosingCountedAmount < 0 {
		return CashSessionView{}, fmt.Errorf("payment/service: closing_counted_amount must not be negative")
	}
	if err := domain.ValidateDenominations(req.Denominations, req.ClosingCountedAmount); err != nil {
		return CashSessionView{}, fmt.Errorf("payment/service: %w", err)
	}

	var view CashSessionView
	err := s.db.WithTenantTx(ctx, principal.TenantID, func(tx pgx.Tx) error {
		current, err := s.sessions.GetByIDForUpdate(ctx, tx, principal.TenantID, sessionID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, current.BranchID); err != nil {
			return err
		}
		session, err := s.sessions.SubmitClosingCount(ctx, tx, principal.TenantID, sessionID, current.Status,
			req.ClosingCountedAmount, req.Denominations, req.Notes)
		if err != nil {
			return err
		}
		view, err = s.buildView(ctx, tx, session)
		return err
	})
	if errors.Is(err, repo.ErrNotFound) {
		return CashSessionView{}, pub.ErrNotFound
	}
	if err != nil {
		return CashSessionView{}, fmt.Errorf("payment/service: submit closing count: %w", err)
	}
	return view, nil
}

// Close validates the close guard and finalises the session. It only ever
// acts on a session already in closing_control (a submitted closing count) —
// callers that skip SubmitClosingCount get a clear invalid-transition error
// via domain.Transition rather than a confusing guard failure.
func (s *CashSessionService) Close(ctx context.Context, principal auth.Principal, sessionID uuid.UUID) (CashSessionView, error) {
	if sessionID == uuid.Nil {
		return CashSessionView{}, fmt.Errorf("payment/service: session id is required")
	}

	var view CashSessionView
	err := s.db.WithTenantTx(ctx, principal.TenantID, func(tx pgx.Tx) error {
		current, err := s.sessions.GetByIDForUpdate(ctx, tx, principal.TenantID, sessionID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, current.BranchID); err != nil {
			return err
		}
		if current.Status != domain.CashSessionClosingControl {
			return fmt.Errorf("payment/service: cash session is %q, submit a closing count first: %w",
				current.Status, domain.ErrInvalidCashSessionTransition)
		}

		reasons, err := s.cannotClose(ctx, tx, principal.TenantID, current.BranchID)
		if err != nil {
			return err
		}
		if len(reasons) > 0 {
			return &CashSessionCannotCloseError{Reasons: reasons}
		}

		session, err := s.sessions.Close(ctx, tx, principal.TenantID, sessionID, current.Status, principal.PersonID)
		if err != nil {
			return err
		}
		view, err = s.buildView(ctx, tx, session)
		return err
	})
	if errors.Is(err, repo.ErrNotFound) {
		return CashSessionView{}, pub.ErrNotFound
	}
	if err != nil {
		return CashSessionView{}, fmt.Errorf("payment/service: close cash session: %w", err)
	}
	return view, nil
}

// cannotClose is the single guard collecting every reason a session must not
// close yet, mirroring Odoo's _cannot_close_session. Today it has one rule
// (ADR-DATA-008's project-specific requirement): the branch must have zero
// pending fiscal submissions, because money state is unknown until the ÖKC
// resolves. It reuses FiscalStatusRepo.ListPendingByBranch — the same query
// the branch-wide fiscal-pending poll and reconciler already use — rather than
// writing a second one.
//
// Deliberately out of scope (per the task this guard was built under):
// kuruş yuvarlama, rescue/recovery sessions, and the old-session warning job.
// Adding a new blocking rule later means appending here, not scattering a
// second check elsewhere.
func (s *CashSessionService) cannotClose(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID) ([]string, error) {
	var reasons []string

	pending, err := s.fiscalRepo.ListPendingByBranch(ctx, tx, tenantID, branchID)
	if err != nil {
		return nil, fmt.Errorf("payment/service: cannot-close guard: %w", err)
	}
	if len(pending) > 0 {
		reasons = append(reasons, fmt.Sprintf(
			"branch has %d pending fiscal submission(s); money state is unknown until the ÖKC resolves them",
			len(pending)))
	}

	return reasons, nil
}
