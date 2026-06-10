// Package service implements payment business logic.
package service

import (
	"context"
	"errors"
	"fmt"

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
	db          *db.Pool
	paymentRepo *repo.PaymentRepo
	fiscal      domain.FiscalDeviceAdapter
	logger      *zap.Logger
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	DB          *db.Pool
	PaymentRepo *repo.PaymentRepo
	Fiscal      domain.FiscalDeviceAdapter
	Logger      *zap.Logger
}

func NewPaymentService(p Params) *PaymentService {
	return &PaymentService{
		db:          p.DB,
		paymentRepo: p.PaymentRepo,
		fiscal:      p.Fiscal,
		logger:      p.Logger,
	}
}

// RegisterSaleRequest carries the inputs for a new payment.
type RegisterSaleRequest struct {
	TenantID       uuid.UUID
	BranchID       uuid.UUID
	CheckID        *uuid.UUID
	IdempotencyKey string
	Method         domain.PaymentMethod
	AmountTotal    int64
	Currency       string
}

// RegisterSale creates a payment, calls the fiscal adapter, and persists a receipt.
// ADR-FISCAL-001: fiscal adapter is always called, even for mock devices.
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
		// Idempotency: return the existing payment if the key was already used.
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
			return fmt.Errorf("payment/service: create payment: %w", err)
		}

		// ADR-FISCAL-001: fiscal registration is mandatory for every payment.
		receipt, err := s.fiscal.RegisterSale(ctx, domain.FiscalSale{
			TenantID:    req.TenantID,
			PaymentID:   payment.ID,
			AmountTotal: req.AmountTotal,
			Currency:    req.Currency,
			Method:      req.Method,
		})
		if err != nil {
			return fmt.Errorf("payment/service: fiscal registration: %w", err)
		}
		receipt.TenantID = req.TenantID
		receipt.PaymentID = payment.ID

		receiptID, err := s.paymentRepo.InsertFiscalReceipt(ctx, tx, receipt)
		if err != nil {
			return fmt.Errorf("payment/service: persist fiscal receipt: %w", err)
		}

		if err := s.paymentRepo.Complete(ctx, tx, payment.ID, receiptID); err != nil {
			return fmt.Errorf("payment/service: complete payment: %w", err)
		}
		payment.Status = domain.PaymentStatusCompleted
		payment.FiscalReceiptID = &receiptID

		return repo.InsertOutbox(ctx, tx, req.TenantID, "payment", payment.ID.String(), "payment.completed", map[string]any{
			"tenant_id":    req.TenantID,
			"payment_id":   payment.ID,
			"check_id":     req.CheckID,
			"method":       req.Method,
			"amount_total": req.AmountTotal,
			"currency":     req.Currency,
		})
	})
	if err != nil {
		return domain.Payment{}, fmt.Errorf("payment/service: register sale: %w", err)
	}
	return payment, nil
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
