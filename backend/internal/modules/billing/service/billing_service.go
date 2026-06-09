// Package service implements billing business logic.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/billing/domain"
	"onlinemenu.tr/internal/modules/billing/repo"
	"onlinemenu.tr/internal/platform/db"
)

// BillingService orchestrates invoice generation and submission.
type BillingService struct {
	db          *db.Pool
	invoiceRepo *repo.InvoiceRepo
	adapter     domain.BillingAdapter
	logger      *zap.Logger
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	DB          *db.Pool
	InvoiceRepo *repo.InvoiceRepo
	Adapter     domain.BillingAdapter
	Logger      *zap.Logger
}

// New constructs a BillingService.
func New(p Params) *BillingService {
	return &BillingService{
		db:          p.DB,
		invoiceRepo: p.InvoiceRepo,
		adapter:     p.Adapter,
		logger:      p.Logger,
	}
}

// GenerateInvoiceRequest carries the inputs for creating and submitting an invoice.
type GenerateInvoiceRequest struct {
	TenantID       uuid.UUID
	BranchID       uuid.UUID
	InvoiceType    domain.InvoiceType
	IdempotencyKey string
	CheckID        *uuid.UUID
	PaymentID      *uuid.UUID
	// Supplier data — typically from tenant profile
	SupplierVKN   string
	SupplierName  string
	SupplierAlias string
	// Customer data — may be looked up via adapter.CheckRecipient
	CustomerVKN  string
	CustomerName string
	// Items to invoice
	Items []InvoiceItemRequest
}

// InvoiceItemRequest is one line on the invoice request.
type InvoiceItemRequest struct {
	ProductID       *uuid.UUID
	ProductName     string
	Quantity        int32
	UnitPriceAmount int64 // KDV Hariç kuruş
	TaxRateBPS      int32 // e.g. 800 = 8%
}

// ErrIdempotent is returned when the idempotency key already exists.
var ErrIdempotent = errors.New("billing/service: idempotency key already used")

// GenerateInvoice creates an invoice record, assigns a number, builds UBL, and submits to provider.
// Idempotent: if the key was used before the existing invoice is returned without re-submission.
func (s *BillingService) GenerateInvoice(ctx context.Context, req GenerateInvoiceRequest) (domain.Invoice, error) {
	if req.IdempotencyKey == "" {
		return domain.Invoice{}, fmt.Errorf("billing/service: idempotency key required")
	}
	if req.SupplierVKN == "" {
		return domain.Invoice{}, fmt.Errorf("billing/service: supplier VKN required")
	}

	// Resolve customer alias for e-fatura (if VKN provided and alias not supplied).
	customerAlias := ""
	if req.InvoiceType == domain.InvoiceTypeEFatura && req.CustomerVKN != "" {
		info, err := s.adapter.CheckRecipient(ctx, domain.CheckRecipientRequest{
			TenantID: req.TenantID.String(),
			VKN:      req.CustomerVKN,
		})
		if err == nil && info.IsRegistered {
			customerAlias = info.Alias
			if req.CustomerName == "" {
				req.CustomerName = info.CompanyName
			}
		}
	}

	items := buildItems(req.Items)
	totals := calculateTotals(items)

	inv := domain.Invoice{
		TenantID:           req.TenantID,
		BranchID:           req.BranchID,
		InvoiceType:        req.InvoiceType,
		CheckID:            req.CheckID,
		PaymentID:          req.PaymentID,
		IdempotencyKey:     req.IdempotencyKey,
		GibUUID:            uuid.New(),
		SupplierVKN:        req.SupplierVKN,
		SupplierName:       req.SupplierName,
		SupplierAlias:      req.SupplierAlias,
		CustomerVKN:        req.CustomerVKN,
		CustomerName:       req.CustomerName,
		CustomerAlias:      customerAlias,
		AmountExcludingTax: totals.amountExcludingTax,
		TaxAmount:          totals.taxAmount,
		AmountTotal:        totals.amountTotal,
		Currency:           "TRY",
		IssueDate:          time.Now().UTC(),
		Items:              items,
	}

	var created domain.Invoice
	err := s.db.WithTenantTx(ctx, req.TenantID, func(tx pgx.Tx) error {
		// Idempotency check.
		existing, lookupErr := s.invoiceRepo.GetByIdempotencyKey(ctx, tx, req.TenantID, req.IdempotencyKey)
		if lookupErr == nil {
			created = existing
			return nil
		}
		if !errors.Is(lookupErr, repo.ErrNotFound) {
			return fmt.Errorf("billing/service: idempotency lookup: %w", lookupErr)
		}

		// Assign invoice number.
		seq, seqErr := s.invoiceRepo.NextInvoiceSequence(ctx, tx, req.TenantID, inv.IssueDate.Year())
		if seqErr != nil {
			return seqErr
		}
		inv.InvoiceNumber = fmt.Sprintf("ONM%d%09d", inv.IssueDate.Year(), seq)

		persisted, createErr := s.invoiceRepo.Create(ctx, tx, inv)
		if createErr != nil {
			return createErr
		}
		created = persisted
		return nil
	})
	if err != nil {
		return domain.Invoice{}, err
	}

	// Submit to the billing provider outside the DB transaction.
	// A failure here leaves the invoice in "draft" status; the caller can retry.
	if created.Status == domain.InvoiceStatusDraft && created.ExternalID == "" {
		submitResult, submitErr := s.adapter.SubmitInvoice(ctx, created)
		if submitErr != nil {
			s.logger.Warn("billing: submit invoice failed",
				zap.String("invoice_id", created.ID.String()),
				zap.Error(submitErr))
			// Non-fatal: invoice is persisted, submission can be retried.
		} else {
			now := submitResult.SubmittedAt
			updateErr := s.db.WithTenantTx(ctx, req.TenantID, func(tx pgx.Tx) error {
				return s.invoiceRepo.UpdateStatus(ctx, tx,
					created.ID,
					domain.InvoiceStatusSubmitted,
					submitResult.ExternalID,
					&now, nil, nil, "",
				)
			})
			if updateErr != nil {
				s.logger.Error("billing: update invoice status after submit",
					zap.String("invoice_id", created.ID.String()),
					zap.Error(updateErr))
			} else {
				created.Status = domain.InvoiceStatusSubmitted
				created.ExternalID = submitResult.ExternalID
				created.SubmittedAt = &now
			}
		}
	}

	return created, nil
}

// GetInvoice returns a single invoice by ID.
func (s *BillingService) GetInvoice(ctx context.Context, tenantID, invoiceID uuid.UUID) (domain.Invoice, error) {
	var inv domain.Invoice
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var txErr error
		inv, txErr = s.invoiceRepo.GetByID(ctx, tx, invoiceID)
		return txErr
	})
	if errors.Is(err, repo.ErrNotFound) {
		return domain.Invoice{}, ErrNotFound
	}
	return inv, err
}

// ListInvoices returns invoices for a tenant with pagination.
func (s *BillingService) ListInvoices(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]domain.Invoice, error) {
	var invoices []domain.Invoice
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var txErr error
		invoices, txErr = s.invoiceRepo.List(ctx, tx, tenantID, limit, offset)
		return txErr
	})
	return invoices, err
}

// RetrySubmission re-submits an invoice that failed to reach the provider.
// Only invoices in "draft" or "pending_submission" status can be retried.
func (s *BillingService) RetrySubmission(ctx context.Context, tenantID, invoiceID uuid.UUID) (domain.Invoice, error) {
	inv, err := s.GetInvoice(ctx, tenantID, invoiceID)
	if err != nil {
		return domain.Invoice{}, err
	}

	if inv.Status != domain.InvoiceStatusDraft && inv.Status != domain.InvoiceStatusPendingSubmission {
		return domain.Invoice{}, fmt.Errorf("billing/service: invoice %s cannot be retried in status %s", invoiceID, inv.Status)
	}

	submitResult, submitErr := s.adapter.SubmitInvoice(ctx, inv)
	if submitErr != nil {
		return inv, fmt.Errorf("billing/service: retry submission: %w", submitErr)
	}

	now := submitResult.SubmittedAt
	updateErr := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.invoiceRepo.UpdateStatus(ctx, tx,
			inv.ID,
			domain.InvoiceStatusSubmitted,
			submitResult.ExternalID,
			&now, nil, nil, "",
		)
	})
	if updateErr != nil {
		return inv, updateErr
	}

	inv.Status = domain.InvoiceStatusSubmitted
	inv.ExternalID = submitResult.ExternalID
	inv.SubmittedAt = &now
	return inv, nil
}

// ErrNotFound is returned when an invoice cannot be found.
var ErrNotFound = errors.New("billing/service: not found")

// invoiceTotals holds calculated monetary totals.
type invoiceTotals struct {
	amountExcludingTax int64
	taxAmount          int64
	amountTotal        int64
}

func buildItems(reqs []InvoiceItemRequest) []domain.InvoiceItem {
	items := make([]domain.InvoiceItem, len(reqs))
	for i, r := range reqs {
		lineTotal := int64(r.Quantity) * r.UnitPriceAmount
		taxAmount := lineTotal * int64(r.TaxRateBPS) / 10000
		items[i] = domain.InvoiceItem{
			ProductID:       r.ProductID,
			ProductName:     r.ProductName,
			Quantity:        r.Quantity,
			UnitPriceAmount: r.UnitPriceAmount,
			TaxRateBPS:      r.TaxRateBPS,
			LineTotal:       lineTotal,
			TaxAmount:       taxAmount,
		}
	}
	return items
}

func calculateTotals(items []domain.InvoiceItem) invoiceTotals {
	var t invoiceTotals
	for _, item := range items {
		t.amountExcludingTax += item.LineTotal
		t.taxAmount += item.TaxAmount
	}
	t.amountTotal = t.amountExcludingTax + t.taxAmount
	return t
}
