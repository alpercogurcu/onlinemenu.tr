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
	"onlinemenu.tr/internal/platform/db"
)

// PaymentService orchestrates payment creation, fiscal registration, and outbox publication.
type PaymentService struct {
	db             *db.Pool
	paymentRepo    *repo.PaymentRepo
	submissionRepo *repo.FiscalSubmissionRepo
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
	Fiscal         domain.FiscalDeviceAdapter
	Logger         *zap.Logger
}

func NewPaymentService(p Params) *PaymentService {
	return &PaymentService{
		db:             p.DB,
		paymentRepo:    p.PaymentRepo,
		submissionRepo: p.SubmissionRepo,
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
	Currency       string
	Lines          []domain.FiscalLine
	Meta           domain.FiscalMeta
	TerminalSerial string
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
		return domain.Payment{}, fmt.Errorf("payment/service: idempotency key is required")
	}
	if !req.Method.Valid() {
		return domain.Payment{}, fmt.Errorf("payment/service: invalid method %q", req.Method)
	}
	if req.AmountTotal <= 0 {
		return domain.Payment{}, fmt.Errorf("payment/service: amount_total must be positive")
	}
	if req.Currency == "" {
		req.Currency = "TRY"
	}

	var payment domain.Payment
	err := s.db.WithTenantTx(ctx, req.TenantID, func(tx pgx.Tx) error {
		// Idempotency fast path: return the existing payment if the key was already used.
		existing, err := s.paymentRepo.GetByIdempotencyKey(ctx, tx, req.TenantID, req.IdempotencyKey)
		if err == nil {
			payment = existing
			return nil
		}
		if !errors.Is(err, repo.ErrNotFound) {
			return fmt.Errorf("payment/service: check idempotency: %w", err)
		}

		payment, err = s.paymentRepo.Create(ctx, tx, domain.Payment{
			TenantID:       req.TenantID,
			BranchID:       req.BranchID,
			CheckID:        req.CheckID,
			IdempotencyKey: req.IdempotencyKey,
			Method:         req.Method,
			AmountTotal:    req.AmountTotal,
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
func buildFiscalSale(submissionID uuid.UUID, payment domain.Payment, req RegisterSaleRequest) domain.FiscalSale {
	lines := req.Lines
	if len(lines) == 0 {
		lines = []domain.FiscalLine{{
			Name:             "Satis",
			UnitPriceMinor:   req.AmountTotal,
			QuantityMilli:    1000,
			TaxRatePermyriad: 0,
			CategoryID:       uuid.Nil,
			Unit:             "C62",
		}}
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
		Meta: req.Meta,
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

	err := s.db.WithTenantTx(ctx, res.TenantID, func(tx pgx.Tx) error {
		transitioned, err := s.submissionRepo.MarkResult(
			ctx, tx, res.SubmissionID, res.Status, res.Raw, res.FailureReason, res.CompletedAt,
		)
		if err != nil {
			return fmt.Errorf("payment/service: mark submission result: %w", err)
		}
		if !transitioned {
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
	issuedAt := res.CompletedAt
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

// ListByTenant returns paginated payments for a tenant.
func (s *PaymentService) ListByTenant(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]domain.Payment, error) {
	var payments []domain.Payment
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		payments, err = s.paymentRepo.ListByTenant(ctx, tx, tenantID, limit, offset)
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
