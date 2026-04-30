// Package public exposes the tenant module's contract to other modules.
// Imports of internal tenant packages from outside the tenant module are
// forbidden by go-arch-lint.
package public

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Plan represents the subscription tier for a tenant.
type Plan string

const (
	PlanStarter    Plan = "starter"
	PlanPro        Plan = "pro"
	PlanEnterprise Plan = "enterprise"
)

// IdentityType distinguishes corporate entities from sole traders.
type IdentityType string

const (
	IdentityKurumsal IdentityType = "kurumsal"
	IdentityBireysel IdentityType = "bireysel"
)

// OwnershipType classifies the relationship between a branch and the tenant.
type OwnershipType string

const (
	OwnershipSube      OwnershipType = "sube"
	OwnershipFranchise OwnershipType = "franchise"
)

// OperationType describes the primary business activity of a branch.
type OperationType string

const (
	OperationRestoran  OperationType = "restoran"
	OperationBar       OperationType = "bar"
	OperationMarket    OperationType = "market"
	OperationFoodTruck OperationType = "food_truck"
	OperationImalat    OperationType = "imalat"
	OperationDepo      OperationType = "depo"
)

// DocumentType enumerates the categories of legal documents a tenant may upload.
type DocumentType string

const (
	DocVergiLevhasi      DocumentType = "vergi_levhasi"
	DocTicaretSicil      DocumentType = "ticaret_sicil"
	DocImzaSirkuleri     DocumentType = "imza_sirkuleri"
	DocFaaliyetBelgesi   DocumentType = "faaliyet_belgesi"
	DocGidaSicil         DocumentType = "gida_sicil"
	DocIsyeriAcmaRuhsati DocumentType = "isyeri_acma_ruhsati"
	DocOther             DocumentType = "other"
)

// DocumentStatus tracks the verification lifecycle of an uploaded document.
type DocumentStatus string

const (
	DocStatusPending  DocumentStatus = "pending"
	DocStatusVerified DocumentStatus = "verified"
	DocStatusRejected DocumentStatus = "rejected"
	DocStatusExpired  DocumentStatus = "expired"
)

// Address represents a legal or billing address.
type Address struct {
	Line1      string
	City       string
	District   string
	PostalCode string
	// Country holds the ISO 3166-1 alpha-2 code; defaults to "TR".
	Country string
}

// SupplyRule specifies which warehouse supplies a branch and at what priority.
type SupplyRule struct {
	WarehouseID uuid.UUID `json:"warehouse_id"`
	Priority    int       `json:"priority"`
}

// Tenant is the read-only projection that other modules may reference.
// It includes legal identity fields added in migration 000002.
type Tenant struct {
	ID             uuid.UUID
	Name           string
	LegalName      string
	TradeName      string
	Slug           string
	Plan           Plan
	EnabledModules []string
	IdentityType   IdentityType
	TaxNo          string
	TaxOffice      string
	MersisNo       string
	Address        Address
	Phone          string
	ContactEmail   string
	IsActive       bool
}

// Branch is the read-only projection of a physical location belonging to a tenant.
// OwnershipType and OperationType fields are added in migration 000002.
type Branch struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	Name          string
	Slug          string
	OwnershipType OwnershipType
	OperationType OperationType
	// SupplyRules is unmarshalled from the JSONB column on the branches table.
	SupplyRules []SupplyRule
	Phone       string
	Address     Address
	IsActive    bool
}

// Document is the Go projection of the tenant_documents table.
// The actual file lives in MinIO; this struct holds metadata only.
type Document struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	DocumentType  DocumentType
	FileKey       string
	FileName      string
	FileSize      int64
	MimeType      string
	Status        DocumentStatus
	RejectionNote string
	ValidFrom     *time.Time
	ValidUntil    *time.Time
	CreatedAt     time.Time
}

// TenantReader allows other modules to look up tenant and branch data
// without importing tenant internals. Implemented by tenant.Service.
type TenantReader interface {
	GetByID(ctx context.Context, tenantID uuid.UUID) (Tenant, error)
	GetBranch(ctx context.Context, tenantID, branchID uuid.UUID) (Branch, error)
	IsModuleEnabled(ctx context.Context, tenantID uuid.UUID, module string) (bool, error)
}

// TenantWriter is reserved for platform-level code (onboarding, admin).
// Regular modules must not depend on this interface.
type TenantWriter interface {
	Create(ctx context.Context, t Tenant) (Tenant, error)
	Update(ctx context.Context, t Tenant) (Tenant, error)
	Deactivate(ctx context.Context, tenantID uuid.UUID) error
}

// DocumentStore manages document metadata. File upload/download goes through
// platform/storage; this interface covers only the DB-side lifecycle.
type DocumentStore interface {
	ListDocuments(ctx context.Context, tenantID uuid.UUID) ([]Document, error)
	GetDocument(ctx context.Context, tenantID, docID uuid.UUID) (Document, error)
	CreateDocument(ctx context.Context, doc Document) (Document, error)
	UpdateDocumentStatus(ctx context.Context, tenantID, docID uuid.UUID, status DocumentStatus, note string) error
	// DeleteDocument performs a soft delete.
	DeleteDocument(ctx context.Context, tenantID, docID uuid.UUID) error
}

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = tenantNotFoundError{}

type tenantNotFoundError struct{}

func (tenantNotFoundError) Error() string { return "tenant: not found" }
