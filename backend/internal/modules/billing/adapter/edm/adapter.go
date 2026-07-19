package edm

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/billing/domain"
)

// Config holds EDM Bilisim connection settings injected at adapter construction.
type Config struct {
	Endpoint string // EDM SOAP endpoint URL (e.g. https://edm.com.tr/api/EFaturaEDM.svc)
	// CredentialsFn resolves the EDM username/password for a given tenant from Vault.
	// In production this calls platform/vault; for tests it may return static values.
	CredentialsFn func(tenantID uuid.UUID) (username, password string, err error)
}

// Adapter implements domain.BillingAdapter using EDM Bilisim SOAP API.
type Adapter struct {
	c        *client
	sessions *sessionManager
	cfg      Config
}

// New constructs an EDM Adapter. The Redis client is used for SOAP session caching.
// logger may be nil, in which case a no-op logger is used.
func New(cfg Config, redisClient *redis.Client, logger *zap.Logger) *Adapter {
	if logger == nil {
		logger = zap.NewNop()
	}
	c := newClient(cfg.Endpoint)
	return &Adapter{
		c:   c,
		cfg: cfg,
		sessions: &sessionManager{
			c:      c,
			redis:  redisClient,
			creds:  cfg.CredentialsFn,
			logger: logger,
		},
	}
}

// CheckRecipient looks up the VKN on GİB to determine e-invoice eligibility.
func (a *Adapter) CheckRecipient(ctx context.Context, req domain.CheckRecipientRequest) (domain.RecipientInfo, error) {
	tenantID, err := uuid.Parse(req.TenantID)
	if err != nil {
		return domain.RecipientInfo{}, fmt.Errorf("edm: invalid tenantID: %w", err)
	}

	result, err := a.checkUser(ctx, tenantID, req.VKN)
	if err != nil {
		return domain.RecipientInfo{}, fmt.Errorf("edm: check recipient: %w", err)
	}

	return domain.RecipientInfo{
		VKN:          result.Identifier,
		Alias:        result.Alias,
		CompanyName:  result.Title,
		IsRegistered: result.Alias != "",
	}, nil
}

// SubmitInvoice builds a UBL XML document and sends it to EDM.
func (a *Adapter) SubmitInvoice(ctx context.Context, inv domain.Invoice) (domain.SubmitResult, error) {
	xmlContent, err := BuildInvoiceXML(inv)
	if err != nil {
		return domain.SubmitResult{}, fmt.Errorf("edm: build UBL XML: %w", err)
	}

	isEArchive := inv.InvoiceType == domain.InvoiceTypeEArsiv
	result, err := a.sendInvoice(
		ctx,
		inv.TenantID,
		inv.SupplierAlias,
		inv.CustomerAlias,
		inv.SupplierVKN,
		inv.CustomerVKN,
		inv.GibUUID.String(),
		xmlContent,
		isEArchive,
	)
	if err != nil {
		return domain.SubmitResult{}, fmt.Errorf("edm: submit invoice: %w", err)
	}

	return domain.SubmitResult{
		ExternalID:  result.IntlTxnID,
		SubmittedAt: time.Now().UTC(),
	}, nil
}

// GetInvoiceStatus retrieves the current GİB status by EDM transaction ID.
func (a *Adapter) GetInvoiceStatus(ctx context.Context, externalID string) (domain.InvoiceStatusResult, error) {
	// externalID may be either the INTL_TXN_ID or the GİB UUID — EDM accepts both.
	// For status polling we use the GİB UUID stored in Invoice.GibUUID.
	// Callers must pass the GİB UUID string here.
	result, err := a.getInvoiceStatus(ctx, uuid.Nil, externalID)
	if err != nil {
		return domain.InvoiceStatusResult{}, fmt.Errorf("edm: get invoice status: %w", err)
	}

	return domain.InvoiceStatusResult{
		Status:      result.Status,
		Description: result.StatusDesc,
	}, nil
}

var _ domain.BillingAdapter = (*Adapter)(nil)
