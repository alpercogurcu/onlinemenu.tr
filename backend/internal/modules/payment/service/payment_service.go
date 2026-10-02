// Package service implements payment business logic.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/repo"
	pospub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/platform/db"
)

// PaymentService orchestrates payment creation, fiscal registration, and outbox publication.
type PaymentService struct {
	db             *db.Pool
	paymentRepo    *repo.PaymentRepo
	submissionRepo *repo.FiscalSubmissionRepo
	statusRepo     *repo.FiscalStatusRepo
	sessionRepo    *repo.CashSessionRepo
	checks         pospub.CheckWriteGuard
	rounding       pospub.BranchRoundingPolicyReader
	fiscal         domain.FiscalDeviceAdapter
	adapterType    string
	logger         *zap.Logger
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	DB             *db.Pool
	PaymentRepo    *repo.PaymentRepo
	SubmissionRepo *repo.FiscalSubmissionRepo
	StatusRepo     *repo.FiscalStatusRepo
	SessionRepo    *repo.CashSessionRepo
	// Checks is pos's guard for "may this check still receive money"
	// (ADR-AUTH-001 layer 3 is about who acts; this is about what they act
	// on). It is an interface from pos/public: payment never reads a pos
	// table. Unit/integration tests that construct Params directly may leave
	// it nil, in which case a sale naming a check_id is refused rather than
	// silently unguarded — see RegisterSale.
	Checks pospub.CheckWriteGuard
	// Rounding reads the branch's cash-rounding policy from pos (beşli
	// yuvarlama). Required in the fx graph so a wiring miss fails at startup;
	// tests that build Params by hand and never round may leave it nil — a
	// rounded sale then fails closed as a server fault, never unvetted.
	Rounding pospub.BranchRoundingPolicyReader
	Fiscal   domain.FiscalDeviceAdapter
	Logger   *zap.Logger
}

func NewPaymentService(p Params) *PaymentService {
	return &PaymentService{
		db:             p.DB,
		paymentRepo:    p.PaymentRepo,
		submissionRepo: p.SubmissionRepo,
		statusRepo:     p.StatusRepo,
		sessionRepo:    p.SessionRepo,
		checks:         p.Checks,
		rounding:       p.Rounding,
		fiscal:         p.Fiscal,
		adapterType:    adapterTypeOf(p.Fiscal),
		logger:         p.Logger,
	}
}

// adapterTypeOf resolves the value stored in fiscal_submissions.adapter_type,
// which routes a claimed submission back to the driver that owns it. Real
// drivers report their own type; only the mock is recognised structurally.
func adapterTypeOf(a domain.FiscalDeviceAdapter) string {
	if named, ok := a.(interface{ AdapterType() string }); ok {
		return named.AdapterType()
	}
	if _, ok := a.(domain.MockFiscalAdapter); ok {
		return "mock"
	}
	return "unknown"
}

// PaymentService is the sink adapters deliver normalized results to.
var _ domain.FiscalResultSink = (*PaymentService)(nil)

// RegisterSaleRequest carries the inputs for a new payment. Lines describe the
// basket the fiscal device must print; Meta is display metadata. TerminalSerial
// optionally pins the submission to one device (vendors that broadcast a basket
// to every terminal in a branch ignore it).
type RegisterSaleRequest struct {
	TenantID       uuid.UUID
	BranchID       uuid.UUID
	CheckID        *uuid.UUID
	IdempotencyKey string
	Method         domain.PaymentMethod
	AmountTotal    int64
	// RoundingAmount is the cash-rounding concession (beşli yuvarlama): the
	// check is settled by AmountTotal + RoundingAmount while AmountTotal is
	// what the customer pays. Zero for an ordinary payment.
	RoundingAmount int64
	Currency       string
	Lines          []domain.FiscalLine
	Meta           domain.FiscalMeta
	TerminalSerial string
}

// cashPaymentRequiresOpenSession reports whether method physically moves cash
// through the branch's drawer and therefore must be reconciled by an open
// cash session (ADR-DATA-008). Only PaymentMethodCash does:
//   - PaymentMethodTerminal settles over the ÖKC card rail; the physical
//     drawer is never touched.
//   - PaymentMethodMealCard settles with the meal-card vendor, not cash.
//   - PaymentMethodComp / PaymentMethodNoCharge move no money at all (ikram /
//     ödemesiz).
//   - PaymentMethodOpenAccount defers settlement to a later payment, which
//     will itself pass through this same check when it actually lands.
//
// Written as a positive "== cash" check rather than "!= terminal" deliberately
// mirrors CashSessionRepo.SumCompletedCashPayments's own reasoning: a
// negation would silently exempt this guard for any future PaymentMethod
// added without an explicit decision here.
func cashPaymentRequiresOpenSession(method domain.PaymentMethod) bool {
	return method == domain.PaymentMethodCash
}

// RegisterSale creates a pending payment and enqueues its fiscal submission in
// the same transaction (ADR-FISCAL-002). The device adapter is deliberately NOT
// called here: a real ÖKC takes seconds or minutes to collect the payment, and
// holding a database transaction open for that is unworkable. SubmissionWorker
// picks the row up and drives it to a terminal state through OnFiscalResult.
//
// ADR-FISCAL-001: fiscal registration stays mandatory — the submission row is
// written unconditionally, even for the mock adapter.
// ADR-SEC-003:    IdempotencyKey must be non-empty (enforced by HTTP middleware and here).
func (s *PaymentService) RegisterSale(ctx context.Context, req RegisterSaleRequest) (domain.Payment, error) {
	if req.IdempotencyKey == "" {
		return domain.Payment{}, fmt.Errorf("%w: idempotency key is required", pub.ErrInvalidInput)
	}
	if !req.Method.Valid() {
		return domain.Payment{}, fmt.Errorf("%w: invalid method %q", pub.ErrInvalidInput, req.Method)
	}
	if req.AmountTotal <= 0 {
		return domain.Payment{}, fmt.Errorf("%w: amount_total must be positive", pub.ErrInvalidInput)
	}
	if req.RoundingAmount < 0 {
		return domain.Payment{}, fmt.Errorf("%w: rounding_amount must not be negative", pub.ErrInvalidInput)
	}
	if req.Currency == "" {
		req.Currency = "TRY"
	}

	// The check verdict is obtained BEFORE the transaction opens, for the same
	// reason CheckService.Close reads its payment totals before taking the
	// check lock: the guard runs on another pooled connection, and calling it
	// while holding this module's write transaction risks pool starvation.
	//
	// It is only APPLIED further down, after the idempotency fast path misses
	// — a retry of a payment that was legitimately taken while the check was
	// open must keep returning that payment, not start conflicting the moment
	// the cashier closes the adisyon. Same rule the cash-session guard below
	// follows, and the reason the verdict is carried rather than returned here.
	checkVerdict, err := s.checkVerdictFor(ctx, req)
	if err != nil {
		return domain.Payment{}, err
	}
	// Read here, not under the lock below, for the same pool-starvation reason
	// as the verdict. Residual window: an order placed or cancelled between
	// this read and the lock is not seen — a raised total can yield a
	// spurious 409 (the cashier retries), a lowered one can let a small
	// overpayment through. The double charge this guards against is still
	// closed, because two racing stations read the same total and the lock
	// makes the second one count the first one's payment.
	// Same read-outside, apply-inside split as the check verdict: a replay of
	// a rounded payment must keep returning it even after an admin switched
	// rounding off.
	roundingVerdict, roundingPolicy, err := s.roundingVerdictFor(ctx, req)
	if err != nil {
		return domain.Payment{}, err
	}
	var checkTotal int64
	if checkVerdict == nil && hasCheck(req) {
		if checkTotal, err = s.checks.CheckTotal(ctx, req.TenantID, *req.CheckID); err != nil {
			return domain.Payment{}, fmt.Errorf("payment/service: read check total: %w", err)
		}
	}

	var payment domain.Payment
	err = s.db.WithTenantTx(ctx, req.TenantID, func(tx pgx.Tx) error {
		// Idempotency fast path: return the existing payment if the key was already used.
		existing, err := s.paymentRepo.GetByIdempotencyKey(ctx, tx, req.TenantID, req.IdempotencyKey)
		if err == nil {
			payment = existing
			return nil
		}
		if !errors.Is(err, repo.ErrNotFound) {
			return fmt.Errorf("payment/service: check idempotency: %w", err)
		}

		if checkVerdict != nil {
			return checkVerdict
		}
		if roundingVerdict != nil {
			return roundingVerdict
		}

		if hasCheck(req) {
			replay, err := s.guardAgainstOverpayment(ctx, tx, req, checkTotal, roundingPolicy)
			if err != nil {
				return err
			}
			if replay != nil {
				payment = *replay
				return nil
			}
		}

		// Only a genuinely new registration is gated — never a replay of an
		// already-successful one (the idempotency fast path above already
		// returned by this point for a replay). Checked here, inside the same
		// transaction as the Create below and after the idempotency read: a
		// check outside this transaction would race a concurrent session close
		// between the check and the write (TOCTOU) and would also wrongly gate
		// idempotent retries of a payment that succeeded while a session was open.
		if cashPaymentRequiresOpenSession(req.Method) {
			if _, sessErr := s.sessionRepo.GetActiveByBranchForShare(ctx, tx, req.TenantID, req.BranchID); sessErr != nil {
				if errors.Is(sessErr, repo.ErrNotFound) {
					return pub.ErrNoCashSessionOpen
				}
				return fmt.Errorf("payment/service: check cash session: %w", sessErr)
			}
		}

		payment, err = s.paymentRepo.Create(ctx, tx, domain.Payment{
			TenantID:       req.TenantID,
			BranchID:       req.BranchID,
			CheckID:        req.CheckID,
			IdempotencyKey: req.IdempotencyKey,
			Method:         req.Method,
			AmountTotal:    req.AmountTotal,
			RoundingAmount: req.RoundingAmount,
			Currency:       req.Currency,
		})
		if err != nil {
			// Race: a concurrent request with the same key won the insert between
			// our pre-check and this Create. Postgres aborts this transaction on
			// the unique violation, so we cannot re-query inside it; the caller
			// below re-fetches in a fresh transaction once this one rolls back.
			return fmt.Errorf("payment/service: create payment: %w", err)
		}

		submissionID := uuid.New()
		sale := buildFiscalSale(submissionID, payment, req)
		salePayload, err := json.Marshal(sale)
		if err != nil {
			return fmt.Errorf("payment/service: marshal fiscal sale: %w", err)
		}

		if err := s.submissionRepo.Insert(ctx, tx, repo.FiscalSubmission{
			ID:             submissionID,
			TenantID:       req.TenantID,
			BranchID:       req.BranchID,
			PaymentID:      payment.ID,
			AdapterType:    s.adapterType,
			TerminalSerial: req.TerminalSerial,
			SalePayload:    salePayload,
			// API host clock: the fiscal status poll subtracts this from its own
			// time.Now() to report age_seconds, so both readings must come from
			// the same clock rather than mixing in the database host's now().
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("payment/service: enqueue fiscal submission: %w", err)
		}
		return nil
	})
	if errors.Is(err, repo.ErrDuplicateIdempotencyKey) {
		// The pre-check missed a concurrent winner; by the time Postgres reported
		// the unique violation, the winning transaction had already committed
		// (Postgres blocks the losing INSERT until the winner commits or rolls
		// back). A fresh read is therefore guaranteed to find the completed row.
		return s.fetchExistingByIdempotencyKey(ctx, req.TenantID, req.IdempotencyKey)
	}
	if err != nil {
		return domain.Payment{}, fmt.Errorf("payment/service: register sale: %w", err)
	}
	return payment, nil
}

// guardAgainstOverpayment refuses a sale that would collect more than the
// check still owes. It runs inside RegisterSale's transaction, after the
// idempotency fast path, so a replay never waits on the lock.
//
// The per-check lock is what makes the sum below trustworthy: without it two
// stations both read "nothing paid yet" and both charge in full. Pending
// payments count as collected — the customer has already handed the money
// over; only the fiscal receipt is outstanding — and the sums are the same
// TotalPaidForCheck / PendingTotalForCheck that CheckService.Close reads, so
// the "may still pay" and "may close" decisions cannot disagree.
//
// A non-nil payment return is an idempotent replay discovered under the lock:
// a concurrent request with the same key committed while this one waited, and
// counting that payment against the total would turn a harmless retry into a
// 409.
//
// A rounded sale is also checked here, under the same lock, because both of
// its remaining rules depend on what the check already holds: the payment
// must close the remainder exactly (rounding is only ever granted on the last
// collection), and the check's total concession must stay within the
// branch's ceiling — summed under the lock so two racing rounded sales cannot
// both fit.
func (s *PaymentService) guardAgainstOverpayment(ctx context.Context, tx pgx.Tx, req RegisterSaleRequest, checkTotal int64, policy pospub.RoundingPolicy) (*domain.Payment, error) {
	checkID := *req.CheckID
	if err := s.paymentRepo.LockCheckForPayment(ctx, tx, checkID); err != nil {
		return nil, fmt.Errorf("payment/service: %w", err)
	}
	existing, err := s.paymentRepo.GetByIdempotencyKey(ctx, tx, req.TenantID, req.IdempotencyKey)
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, repo.ErrNotFound) {
		return nil, fmt.Errorf("payment/service: recheck idempotency under lock: %w", err)
	}
	paid, err := s.paymentRepo.TotalPaidForCheck(ctx, tx, req.TenantID, checkID)
	if err != nil {
		return nil, fmt.Errorf("payment/service: %w", err)
	}
	pending, err := s.paymentRepo.PendingTotalForCheck(ctx, tx, req.TenantID, checkID)
	if err != nil {
		return nil, fmt.Errorf("payment/service: %w", err)
	}
	due := checkTotal - paid - pending
	settles := req.AmountTotal + req.RoundingAmount
	if settles > due {
		return nil, pub.ErrPaymentExceedsDue
	}
	if req.RoundingAmount == 0 {
		return nil, nil
	}
	if settles != due {
		return nil, fmt.Errorf("%w: payment settles %d but the check owes %d — rounding only closes the remainder", pub.ErrRoundingNotAllowed, settles, due)
	}
	conceded, err := s.paymentRepo.RoundingTotalForCheck(ctx, tx, req.TenantID, checkID)
	if err != nil {
		return nil, fmt.Errorf("payment/service: %w", err)
	}
	if conceded+req.RoundingAmount > policy.MaxPerCheckMinor {
		return nil, fmt.Errorf("%w: check rounding %d + %d exceeds the branch ceiling %d", pub.ErrRoundingNotAllowed, conceded, req.RoundingAmount, policy.MaxPerCheckMinor)
	}
	return nil, nil
}

// roundingVerdictFor vets a rounded sale against everything that does not
// depend on the check's current payments: the branch policy (method enabled,
// concession below the step, amount a step multiple), the presence of a
// check, and the fiscal adapter.
//
// Like checkVerdictFor it returns (verdict, policy, nil) — the verdict is
// applied after the idempotency fast path — and (nil, _, err) when the policy
// could not be read at all, which is a 500, not a refusal.
//
// The adapter rule implements kasa-rapor-programi G.2: how a real ÖKC
// (Token) spreads a sale-level discount across VAT rates is not confirmed
// yet, so rounding stays limited to the mock adapter until it is. The
// adapter is process-wide, so the rule needs no per-branch lookup.
func (s *PaymentService) roundingVerdictFor(ctx context.Context, req RegisterSaleRequest) (verdict error, policy pospub.RoundingPolicy, failure error) {
	if req.RoundingAmount == 0 {
		return nil, pospub.RoundingPolicy{}, nil
	}
	if !hasCheck(req) {
		return fmt.Errorf("%w: rounding needs a check", pub.ErrRoundingNotAllowed), pospub.RoundingPolicy{}, nil
	}
	if s.adapterType != "mock" {
		return fmt.Errorf("%w: fiscal adapter %q does not support rounding yet", pub.ErrRoundingNotAllowed, s.adapterType), pospub.RoundingPolicy{}, nil
	}
	if s.rounding == nil {
		// Fail closed, as checkVerdictFor does for a nil guard.
		return nil, pospub.RoundingPolicy{}, fmt.Errorf("payment/service: rounding policy reader not wired")
	}
	policy, err := s.rounding.BranchRoundingPolicy(ctx, req.TenantID, req.BranchID)
	if err != nil {
		return nil, pospub.RoundingPolicy{}, fmt.Errorf("payment/service: read rounding policy: %w", err)
	}
	if !roundingEnabledFor(policy, req.Method) {
		return fmt.Errorf("%w: rounding is off for %s at this branch", pub.ErrRoundingNotAllowed, req.Method), policy, nil
	}
	if policy.StepMinor <= 0 || req.RoundingAmount >= policy.StepMinor {
		return fmt.Errorf("%w: rounding %d is not below the step %d", pub.ErrRoundingNotAllowed, req.RoundingAmount, policy.StepMinor), policy, nil
	}
	if req.AmountTotal%policy.StepMinor != 0 {
		return fmt.Errorf("%w: amount %d is not a multiple of the step %d", pub.ErrRoundingNotAllowed, req.AmountTotal, policy.StepMinor), policy, nil
	}
	return nil, policy, nil
}

// roundingEnabledFor maps a payment method to the branch switch that governs
// it. Only cash and card (terminal) can round; every other method is refused
// rather than defaulted, so a future method needs an explicit decision here.
func roundingEnabledFor(policy pospub.RoundingPolicy, method domain.PaymentMethod) bool {
	switch method {
	case domain.PaymentMethodCash:
		return policy.CashEnabled
	case domain.PaymentMethodTerminal:
		return policy.CardEnabled
	default:
		return false
	}
}

func hasCheck(req RegisterSaleRequest) bool {
	return req.CheckID != nil && *req.CheckID != uuid.Nil
}

// checkVerdictFor asks pos whether req's check may still receive money.
//
// It returns (verdict, nil) when pos refused — the caller applies that verdict
// after the idempotency fast path — and (nil, err) when pos could not be asked
// at all, which is a 500, not a conflict. A sale with no check_id (masasız
// satış, paket servis) is unaffected: there is nothing to validate.
func (s *PaymentService) checkVerdictFor(ctx context.Context, req RegisterSaleRequest) (verdict, failure error) {
	if !hasCheck(req) {
		return nil, nil
	}
	if s.checks == nil {
		// Fail closed. A nil guard means the composition root did not wire pos
		// in; silently skipping would restore exactly the production defect
		// this guard exists to close.
		return nil, fmt.Errorf("payment/service: check write guard not wired")
	}
	if err := s.checks.AssertCheckWritable(ctx, req.TenantID, *req.CheckID, req.BranchID); err != nil {
		if verdict := translateCheckGuardErr(err); verdict != nil {
			return verdict, nil
		}
		return nil, fmt.Errorf("payment/service: assert check writable: %w", err)
	}
	return nil, nil
}

// fetchExistingByIdempotencyKey re-reads a payment in a fresh transaction after
// a concurrent duplicate-key conflict aborted the original write transaction.
func (s *PaymentService) fetchExistingByIdempotencyKey(ctx context.Context, tenantID uuid.UUID, key string) (domain.Payment, error) {
	var payment domain.Payment
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		payment, err = s.paymentRepo.GetByIdempotencyKey(ctx, tx, tenantID, key)
		return err
	})
	if err != nil {
		return domain.Payment{}, fmt.Errorf("payment/service: register sale: fetch after conflict: %w", err)
	}
	return payment, nil
}

// buildFiscalSale maps a payment plus its request into the vendor-neutral sale.
//
// When the caller supplies no lines we synthesize a single "Satış" line for the
// full amount so the mock/dev flow keeps working. Real devices demand a
// per-item basket with device-section and tax mapping (ADR-FISCAL-002 §2) and
// their adapters are expected to reject this synthetic line.
//
// A rounded sale prints the covered items at their full price and the
// concession as a sale-level "Yuvarlama" discount, so the receipt total is
// the money actually taken: lines add up to AmountTotal + RoundingAmount,
// TotalMinor and the payment stay AmountTotal.
func buildFiscalSale(submissionID uuid.UUID, payment domain.Payment, req RegisterSaleRequest) domain.FiscalSale {
	lines := req.Lines
	if len(lines) == 0 {
		lines = []domain.FiscalLine{{
			Name:             "Satis",
			UnitPriceMinor:   req.AmountTotal + req.RoundingAmount,
			QuantityMilli:    1000,
			TaxRatePermyriad: 0,
			CategoryID:       uuid.Nil,
			Unit:             "C62",
		}}
	}
	var discount *domain.FiscalAdjust
	if req.RoundingAmount > 0 {
		discount = &domain.FiscalAdjust{
			Description: "Yuvarlama",
			Kind:        domain.FiscalAdjustDiscount,
			Mode:        domain.FiscalAdjustAmount,
			Value:       req.RoundingAmount,
		}
	}
	return domain.FiscalSale{
		SubmissionID: submissionID,
		TenantID:     req.TenantID,
		BranchID:     req.BranchID,
		PaymentID:    payment.ID,
		CheckID:      req.CheckID,
		Currency:     req.Currency,
		TotalMinor:   req.AmountTotal,
		Lines:        lines,
		Payments: []domain.FiscalPayment{{
			Method:      req.Method,
			AmountMinor: req.AmountTotal,
		}},
		Discount: discount,
		Meta:     req.Meta,
	}
}

// OnFiscalResult applies a normalized adapter result: it moves the submission to
// its terminal state and, only if that transition actually happened, performs
// the side effects (receipt, payment status, outbox event).
//
// Idempotency is enforced by MarkResult's guarded UPDATE. A duplicate delivery —
// webhook retry, reconciliation sweep, or a worker re-claim — finds the row
// already terminal, transitions nothing, and returns without any side effect.
func (s *PaymentService) OnFiscalResult(ctx context.Context, res domain.FiscalResult) error {
	if res.TenantID == uuid.Nil {
		return fmt.Errorf("payment/service: fiscal result: tenant id is required")
	}
	if res.SubmissionID == uuid.Nil {
		return fmt.Errorf("payment/service: fiscal result: submission id is required")
	}
	// Every side effect below keys off PaymentID. An adapter that forgets to
	// echo it must fail here, not silently write an orphan receipt.
	if res.PaymentID == uuid.Nil {
		return fmt.Errorf("payment/service: fiscal result: payment id is required")
	}

	// The sink is the single authority for CompletedAt, regardless of what an
	// adapter set: it drives the fiscal status poll's recency window, which
	// must key off THIS server's clock, not a device's. A synchronous or
	// webhook-fed adapter already sets it this way today, but relying on every
	// current and future caller to remember is exactly how an ÖKC with a
	// skewed clock would silently vanish from (or flood) the poll window.
	// DeviceOperationAt is untouched — it carries the device's legal time and
	// has no such requirement.
	res.CompletedAt = time.Now().UTC()

	err := s.db.WithTenantTx(ctx, res.TenantID, func(tx pgx.Tx) error {
		transitioned, err := s.submissionRepo.MarkResult(
			ctx, tx, res.SubmissionID, res.Status, res.Raw, res.FailureReason, res.CompletedAt,
		)
		if err != nil {
			return fmt.Errorf("payment/service: mark submission result: %w", err)
		}
		if !transitioned {
			s.warnIfLostAfterExpire(ctx, tx, res)
			s.logger.Debug("payment: duplicate fiscal result ignored",
				zap.Stringer("submission_id", res.SubmissionID),
				zap.String("status", string(res.Status)),
			)
			return nil
		}

		switch res.Status {
		case domain.FiscalSubmissionCompleted:
			return s.applyCompleted(ctx, tx, res)
		case domain.FiscalSubmissionFailed, domain.FiscalSubmissionExpired:
			return s.applyFailed(ctx, tx, res)
		case domain.FiscalSubmissionVoided:
			return s.applyVoided(ctx, tx, res)
		default:
			return fmt.Errorf("payment/service: fiscal result: unsupported status %q", res.Status)
		}
	})
	if err != nil {
		return fmt.Errorf("payment/service: on fiscal result: %w", err)
	}
	return nil
}

func (s *PaymentService) applyCompleted(ctx context.Context, tx pgx.Tx, res domain.FiscalResult) error {
	// The receipt's legal timestamp is the device's own operation time when the
	// driver reports one; CompletedAt (when this server learned the outcome) is
	// only the fallback for drivers that report none, such as the synchronous
	// mock.
	issuedAt := res.DeviceOperationAt
	if issuedAt.IsZero() {
		issuedAt = res.CompletedAt
	}
	// Unreachable in practice since OnFiscalResult always stamps CompletedAt
	// before reaching here; kept as defense-in-depth in case that invariant
	// ever moves.
	if issuedAt.IsZero() {
		issuedAt = time.Now().UTC()
	}
	receiptID, err := s.paymentRepo.InsertFiscalReceipt(ctx, tx, domain.FiscalReceipt{
		TenantID:      res.TenantID,
		PaymentID:     res.PaymentID,
		DeviceType:    res.DeviceType,
		ReceiptNumber: res.ReceiptNo,
		ZNo:           res.ZNo,
		VendorRef:     res.VendorRef,
		IssuedAt:      issuedAt,
	})
	if err != nil {
		return fmt.Errorf("persist fiscal receipt: %w", err)
	}
	if err := s.paymentRepo.Complete(ctx, tx, res.PaymentID, receiptID); err != nil {
		return fmt.Errorf("complete payment: %w", err)
	}
	return repo.InsertOutbox(ctx, tx, res.TenantID, "payment", res.PaymentID.String(), "payment.completed", map[string]any{
		"tenant_id":     res.TenantID,
		"branch_id":     res.BranchID,
		"payment_id":    res.PaymentID,
		"submission_id": res.SubmissionID,
		"receipt_no":    res.ReceiptNo,
		"device_type":   res.DeviceType,
	})
}

func (s *PaymentService) applyFailed(ctx context.Context, tx pgx.Tx, res domain.FiscalResult) error {
	if err := s.paymentRepo.Fail(ctx, tx, res.PaymentID); err != nil {
		return fmt.Errorf("fail payment: %w", err)
	}
	s.logger.Warn("payment: fiscal registration failed",
		zap.Stringer("payment_id", res.PaymentID),
		zap.Stringer("submission_id", res.SubmissionID),
		zap.String("reason", res.FailureReason),
	)
	return nil
}

func (s *PaymentService) applyVoided(ctx context.Context, tx pgx.Tx, res domain.FiscalResult) error {
	if err := s.paymentRepo.Void(ctx, tx, res.PaymentID); err != nil {
		return fmt.Errorf("void payment: %w", err)
	}
	return repo.InsertOutbox(ctx, tx, res.TenantID, "payment", res.PaymentID.String(), "payment.voided", map[string]any{
		"tenant_id":     res.TenantID,
		"branch_id":     res.BranchID,
		"payment_id":    res.PaymentID,
		"submission_id": res.SubmissionID,
		"reason":        res.FailureReason,
	})
}

// VoidSale cancels a previously registered sale on the device (fiş iptali).
// Like SubmitSale, the adapter may finish synchronously (non-nil result, applied
// immediately) or acknowledge and deliver the result later through the sink.
func (s *PaymentService) VoidSale(ctx context.Context, tenantID, paymentID uuid.UUID) error {
	if !s.fiscal.Capabilities().VoidSale {
		return fmt.Errorf("payment/service: void sale: adapter does not support voiding")
	}

	var sub repo.FiscalSubmission
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		sub, err = s.submissionRepo.GetByPaymentID(ctx, tx, paymentID)
		return err
	})
	if errors.Is(err, repo.ErrNotFound) {
		return pub.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("payment/service: void sale: load submission: %w", err)
	}
	// A replayed void is idempotent success, not an error.
	if sub.Status == domain.FiscalSubmissionVoided {
		return nil
	}
	// Only a registration the device actually completed can be voided. A
	// pending/submitted row races the worker: voiding it while SubmitSale is in
	// flight would let the later 'completed' result be silently dropped by the
	// MarkResult gate, leaving a printed receipt behind a voided payment.
	if sub.Status != domain.FiscalSubmissionCompleted {
		return fmt.Errorf("payment/service: void sale: submission is %q, only completed registrations can be voided", sub.Status)
	}

	res, err := s.fiscal.VoidSale(ctx, domain.FiscalSubmissionRef{
		SubmissionID:   sub.ID,
		TenantID:       sub.TenantID,
		BranchID:       sub.BranchID,
		TerminalSerial: sub.TerminalSerial,
	})
	if err != nil {
		return fmt.Errorf("payment/service: void sale: adapter: %w", err)
	}
	if res == nil {
		return nil // vendor will deliver the void result asynchronously
	}

	stampResultIdentity(res, sub)
	if res.Status == "" {
		res.Status = domain.FiscalSubmissionVoided
	}
	// A device may refuse the void (receipt already closed on a Z report, for
	// example). Never launder that refusal into a voided payment: report it and
	// leave the submission untouched.
	if res.Status != domain.FiscalSubmissionVoided {
		return fmt.Errorf("payment/service: void sale: adapter reported %q: %s", res.Status, res.FailureReason)
	}
	return s.OnFiscalResult(ctx, *res)
}

// stampResultIdentity overwrites the identifiers on an adapter result with the
// values we already know from the submission row. Adapters are not required to
// echo them back, and our copy is authoritative.
func stampResultIdentity(res *domain.FiscalResult, sub repo.FiscalSubmission) {
	res.SubmissionID = sub.ID
	res.TenantID = sub.TenantID
	res.BranchID = sub.BranchID
	res.PaymentID = sub.PaymentID
}

// GetByID returns a payment by its ID.
func (s *PaymentService) GetByID(ctx context.Context, tenantID, id uuid.UUID) (domain.Payment, error) {
	var p domain.Payment
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		p, err = s.paymentRepo.GetByID(ctx, tx, id)
		return err
	})
	if errors.Is(err, repo.ErrNotFound) {
		return domain.Payment{}, pub.ErrNotFound
	}
	if err != nil {
		return domain.Payment{}, fmt.Errorf("payment/service: get by id: %w", err)
	}
	return p, nil
}

// ListByTenant returns paginated payments for a tenant, optionally narrowed
// to one branch (nil = every branch — tenant-scoped callers only; see
// BranchScopeFilter).
func (s *PaymentService) ListByTenant(ctx context.Context, tenantID uuid.UUID, branchID *uuid.UUID, limit, offset int) ([]domain.Payment, error) {
	var payments []domain.Payment
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		payments, err = s.paymentRepo.ListByTenant(ctx, tx, tenantID, branchID, limit, offset)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("payment/service: list by tenant: %w", err)
	}
	return payments, nil
}

// ListByCheck returns completed payments for a check, newest first — used by
// POS to surface previously recorded payments when a cashier reopens a check
// (double-payment guard).
func (s *PaymentService) ListByCheck(ctx context.Context, tenantID, checkID uuid.UUID) ([]domain.Payment, error) {
	var payments []domain.Payment
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		payments, err = s.paymentRepo.ListByCheck(ctx, tx, tenantID, checkID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("payment/service: list by check: %w", err)
	}
	return payments, nil
}

// TotalPaidForCheck returns the sum of completed payments for a check.
func (s *PaymentService) TotalPaidForCheck(ctx context.Context, tenantID, checkID uuid.UUID) (int64, error) {
	var total int64
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		total, err = s.paymentRepo.TotalPaidForCheck(ctx, tx, tenantID, checkID)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("payment/service: total paid for check: %w", err)
	}
	return total, nil
}

// PendingTotalForCheck returns the sum of payments for a check still awaiting a
// fiscal result (see repo.PendingTotalForCheck for the status rationale).
func (s *PaymentService) PendingTotalForCheck(ctx context.Context, tenantID, checkID uuid.UUID) (int64, error) {
	var total int64
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		total, err = s.paymentRepo.PendingTotalForCheck(ctx, tx, tenantID, checkID)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("payment/service: pending total for check: %w", err)
	}
	return total, nil
}
