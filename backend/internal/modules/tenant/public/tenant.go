// Package public exposes the tenant module's contract to other modules.
// Imports of internal tenant packages from outside the tenant module are
// forbidden by go-arch-lint.
package public

import (
	"context"
	"fmt"
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

// BranchDocumentType enumerates document categories specific to a branch.
type BranchDocumentType string

const (
	BranchDocVergiLevhasi        BranchDocumentType = "vergi_levhasi"
	BranchDocIsyeriRuhsati       BranchDocumentType = "isyeri_ruhsati"
	BranchDocKiraSozlesmesi      BranchDocumentType = "kira_sozlesmesi"
	BranchDocFranchiseSozlesmesi BranchDocumentType = "franchise_sozlesmesi"
	BranchDocGidaSicil           BranchDocumentType = "gida_sicil"
	BranchDocIsyeriAcmaRuhsati   BranchDocumentType = "isyeri_acma_ruhsati"
	BranchDocYanginGuvenlik      BranchDocumentType = "yangin_guvenlik"
	BranchDocSaglikSertifikasi   BranchDocumentType = "saglik_sertifikasi"
	BranchDocOther               BranchDocumentType = "other"
)

// BillingProvider identifies the e-invoice / e-archive integration partner.
type BillingProvider string

const (
	ProviderEDM           BillingProvider = "edm"
	ProviderParasut       BillingProvider = "parasut"
	ProviderMikro         BillingProvider = "mikro"
	ProviderLogo          BillingProvider = "logo"
	ProviderIzibiz        BillingProvider = "izibiz"
	ProviderDigitalPlanet BillingProvider = "digital_planet"
)

// IntegratorEnvironment distinguishes GİB test portal from production.
type IntegratorEnvironment string

const (
	EnvTest       IntegratorEnvironment = "test"
	EnvProduction IntegratorEnvironment = "production"
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
// Franchise legal identity fields (IBAN, LegalName, IdentityType, TaxNo, TaxOffice)
// are used when the branch operates as an independent legal entity; if LegalName is
// empty, tenant.LegalName is the authoritative value.
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
	IBAN        string
	// LegalName is the franchise legal name; when empty, tenant.LegalName applies.
	LegalName    string
	IdentityType IdentityType
	TaxNo        string
	TaxOffice    string
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

// BranchDocument is the Go projection of the branch_documents table.
// Structure mirrors Document with an additional BranchID discriminator.
type BranchDocument struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	BranchID      uuid.UUID
	DocumentType  BranchDocumentType
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

// BillingIntegrator holds the configuration for an e-invoice / e-archive provider.
// Sensitive credentials are stored in Vault; only the path is kept here.
// When BranchID is nil the record acts as the tenant-wide default.
type BillingIntegrator struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	BranchID        *uuid.UUID // nil = tenant-wide default
	Provider        BillingProvider
	DisplayName     string
	Config          map[string]any // non-sensitive config (JSON)
	VaultSecretPath string         // path to credentials in Vault
	EfaturaAlias    string         // GİB posta kutusu
	Environment     IntegratorEnvironment
	IsActive        bool
	CreatedAt       time.Time
}

// TimeOfDay holds a wall-clock time at minute precision.
// Using a dedicated type instead of time.Time prevents accidental date comparisons.
type TimeOfDay struct {
	Hour   int // 0–23
	Minute int // 0–59
}

// String returns the time in HH:MM format.
func (t TimeOfDay) String() string {
	return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute)
}

// RegularHours represents a single opening slot for a given day of the week.
// Multiple slots per day are allowed to model split-service schedules (e.g. lunch + dinner).
type RegularHours struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	BranchID  uuid.UUID
	DayOfWeek time.Weekday // time.Monday=1 … time.Sunday=0; DB stores ISO 8601 (1=Mon, 7=Sun)
	OpenTime  *TimeOfDay
	CloseTime *TimeOfDay
	// CrossesMidnight is true when CloseTime is on the following calendar day.
	CrossesMidnight bool
	IsClosed        bool
	SortOrder       int
}

// SpecialHours overrides RegularHours for a specific calendar date.
// Only the date part of SpecialDate is meaningful (time components must be zero, UTC).
type SpecialHours struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	BranchID    uuid.UUID
	SpecialDate time.Time // date only: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	Name        string    // e.g. "Kurban Bayramı 1. Günü"
	OpenTime    *TimeOfDay
	CloseTime   *TimeOfDay
	// CrossesMidnight is true when CloseTime is on the following calendar day.
	CrossesMidnight bool
	IsClosed        bool
}

// TenantReader allows other modules to look up tenant and branch data
// without importing tenant internals. Implemented by tenant.Service.
type TenantReader interface {
	GetByID(ctx context.Context, tenantID uuid.UUID) (Tenant, error)
	GetBranch(ctx context.Context, tenantID, branchID uuid.UUID) (Branch, error)
	IsModuleEnabled(ctx context.Context, tenantID uuid.UUID, module string) (bool, error)
	// GetEffectiveIntegrator returns the active billing integrator for the given branch.
	// If a branch-level record exists it takes precedence; otherwise the tenant default is returned.
	GetEffectiveIntegrator(ctx context.Context, tenantID, branchID uuid.UUID, provider BillingProvider) (BillingIntegrator, error)
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

	ListBranchDocuments(ctx context.Context, tenantID, branchID uuid.UUID) ([]BranchDocument, error)
	CreateBranchDocument(ctx context.Context, doc BranchDocument) (BranchDocument, error)
	UpdateBranchDocumentStatus(ctx context.Context, tenantID, branchID, docID uuid.UUID, status DocumentStatus, note string) error
	DeleteBranchDocument(ctx context.Context, tenantID, branchID, docID uuid.UUID) error
}

// HoursStore manages a branch's operating schedule.
type HoursStore interface {
	// GetRegularHours returns the weekly schedule slots for a branch, ordered by day and sort_order.
	GetRegularHours(ctx context.Context, tenantID, branchID uuid.UUID) ([]RegularHours, error)
	// SetRegularHours atomically replaces the entire weekly schedule for a branch (upsert).
	SetRegularHours(ctx context.Context, tenantID, branchID uuid.UUID, hours []RegularHours) error

	// GetSpecialHours returns all special-day overrides for a branch, ordered by date.
	GetSpecialHours(ctx context.Context, tenantID, branchID uuid.UUID) ([]SpecialHours, error)
	// UpsertSpecialHours inserts or updates a single special-day override.
	UpsertSpecialHours(ctx context.Context, tenantID, branchID uuid.UUID, sh SpecialHours) error
	// DeleteSpecialHours removes the special-day override for the given date.
	DeleteSpecialHours(ctx context.Context, tenantID, branchID uuid.UUID, date time.Time) error

	// IsOpenAt reports whether the branch is open at the given instant.
	// Special hours take precedence over regular hours when both are defined.
	IsOpenAt(ctx context.Context, tenantID, branchID uuid.UUID, at time.Time) (bool, error)
}

// IntegratorStore manages billing integrator configurations.
type IntegratorStore interface {
	ListIntegrators(ctx context.Context, tenantID uuid.UUID) ([]BillingIntegrator, error)
	GetIntegrator(ctx context.Context, tenantID, integratorID uuid.UUID) (BillingIntegrator, error)
	// GetEffectiveIntegrator returns the branch-level integrator when present, falling back to
	// the tenant-wide default for the given provider.
	GetEffectiveIntegrator(ctx context.Context, tenantID, branchID uuid.UUID, provider BillingProvider) (BillingIntegrator, error)
	CreateIntegrator(ctx context.Context, i BillingIntegrator) (BillingIntegrator, error)
	UpdateIntegrator(ctx context.Context, i BillingIntegrator) (BillingIntegrator, error)
	DeleteIntegrator(ctx context.Context, tenantID, integratorID uuid.UUID) error
}

// BranchStore manages branch lifecycle. Exposed so other modules can depend on this
// narrower interface without importing the full service.
type BranchStore interface {
	ListBranches(ctx context.Context, tenantID uuid.UUID) ([]Branch, error)
	CreateBranch(ctx context.Context, b Branch) (Branch, error)
	UpdateBranch(ctx context.Context, b Branch) (Branch, error)
}

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = tenantNotFoundError{}

type tenantNotFoundError struct{}

func (tenantNotFoundError) Error() string { return "tenant: not found" }

// ErrInvalid is returned when input fails domain validation.
var ErrInvalid = tenantInvalidError{}

type tenantInvalidError struct{}

func (tenantInvalidError) Error() string { return "tenant: invalid input" }
