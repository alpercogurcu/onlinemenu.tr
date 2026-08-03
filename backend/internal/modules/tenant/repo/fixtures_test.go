package repo_test

// Shared test fixtures for the tenant module's repo integration tests.
//
// IMPORTANT: tenants.tax_no, tenants.mersis_no and branches.tax_no all carry
// partial UNIQUE indexes (`WHERE tax_no IS NOT NULL`). pub.Tenant.TaxNo /
// pub.Branch.TaxNo are plain Go `string`, so an unset field inserts SQL ''
// (empty string), which IS NOT NULL — every fixture below therefore assigns
// a unique tax_no/mersis_no to avoid cross-test collisions. See the test
// report for why this is itself a reported defect (a real tenant/branch
// onboarded without a tax number collides with the next one).

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	pub "onlinemenu.tr/internal/modules/tenant/public"
	"onlinemenu.tr/internal/modules/tenant/repo"
)

// uniqueSuffix returns a short, collision-resistant string suitable for
// slugs/tax numbers within a single test binary run.
func uniqueSuffix() string {
	return uuid.New().String()[:8]
}

// tod builds a pub.TimeOfDay pointer for opening-hours fixtures.
func tod(h, m int) *pub.TimeOfDay {
	return &pub.TimeOfDay{Hour: h, Minute: m}
}

// mustNewID generates a UUIDv7, consistent with how the service layer mints
// tenant/entity ids (see service.Create's comment on WithTenantTx(newID)).
func mustNewID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	return id
}

// createTenant inserts a fully-formed tenant with a unique slug/tax_no/mersis_no
// and returns the persisted record. It runs inside the tenant's own WithTenantTx,
// exactly as service.Create does.
func createTenant(t *testing.T, ctx context.Context) pub.Tenant {
	t.Helper()
	id := mustNewID(t)
	suffix := uniqueSuffix()

	f := pub.Tenant{
		ID:             id,
		Name:           "Test İşletme " + suffix,
		LegalName:      "Test İşletme A.Ş. " + suffix,
		Slug:           "tenant-" + suffix,
		Plan:           pub.PlanStarter,
		EnabledModules: []string{"pos"},
		IdentityType:   pub.IdentityKurumsal,
		TaxNo:          "TAXNO" + suffix,
		MersisNo:       "MERSIS" + suffix,
		IsActive:       true,
	}

	var created pub.Tenant
	r := repo.NewTenantRepo()
	err := sharedPool.WithTenantTx(ctx, id, func(tx pgx.Tx) error {
		var err error
		created, err = r.Create(ctx, tx, f)
		return err
	})
	require.NoError(t, err)
	return created
}

// createBranch inserts a branch for the given tenant with a unique slug/tax_no.
func createBranch(t *testing.T, ctx context.Context, tenantID uuid.UUID) pub.Branch {
	t.Helper()
	suffix := uniqueSuffix()

	f := pub.Branch{
		TenantID:      tenantID,
		Name:          "Şube " + suffix,
		Slug:          "branch-" + suffix,
		OwnershipType: pub.OwnershipSube,
		OperationType: pub.OperationRestoran,
		IdentityType:  pub.IdentityKurumsal,
		TaxNo:         "BTAXNO" + suffix,
		IsActive:      true,
	}

	var created pub.Branch
	r := repo.NewBranchRepo()
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = r.CreateBranch(ctx, tx, f)
		return err
	})
	require.NoError(t, err)
	return created
}

// createDocument inserts a tenant_documents row for the given tenant.
func createDocument(t *testing.T, ctx context.Context, tenantID uuid.UUID) pub.Document {
	t.Helper()
	suffix := uniqueSuffix()

	f := pub.Document{
		TenantID:     tenantID,
		DocumentType: pub.DocVergiLevhasi,
		FileKey:      "tenants/" + tenantID.String() + "/vergi_levhasi/" + suffix + ".pdf",
		FileName:     "vergi-levhasi.pdf",
		FileSize:     1024,
		MimeType:     "application/pdf",
		Status:       pub.DocStatusPending,
	}

	var created pub.Document
	r := repo.NewDocumentRepo()
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = r.CreateDocument(ctx, tx, f)
		return err
	})
	require.NoError(t, err)
	return created
}

// createBranchDocument inserts a branch_documents row.
func createBranchDocument(t *testing.T, ctx context.Context, tenantID, branchID uuid.UUID) pub.BranchDocument {
	t.Helper()
	suffix := uniqueSuffix()

	f := pub.BranchDocument{
		TenantID:     tenantID,
		BranchID:     branchID,
		DocumentType: pub.BranchDocKiraSozlesmesi,
		FileKey:      "branches/" + branchID.String() + "/kira_sozlesmesi/" + suffix + ".pdf",
		FileName:     "kira-sozlesmesi.pdf",
		FileSize:     2048,
		MimeType:     "application/pdf",
		Status:       pub.DocStatusPending,
	}

	var created pub.BranchDocument
	r := repo.NewDocumentRepo()
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = r.CreateBranchDocument(ctx, tx, f)
		return err
	})
	require.NoError(t, err)
	return created
}

// createIntegrator inserts a billing_integrators row. branchID may be nil for
// a tenant-wide default.
func createIntegrator(t *testing.T, ctx context.Context, tenantID uuid.UUID, branchID *uuid.UUID, provider pub.BillingProvider) pub.BillingIntegrator {
	t.Helper()
	suffix := uniqueSuffix()

	f := pub.BillingIntegrator{
		TenantID:    tenantID,
		BranchID:    branchID,
		Provider:    provider,
		DisplayName: "EDM " + suffix,
		Config:      map[string]any{"company_code": suffix},
		Environment: pub.EnvTest,
		IsActive:    true,
	}

	var created pub.BillingIntegrator
	r := repo.NewIntegratorRepo()
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = r.CreateIntegrator(ctx, tx, f)
		return err
	})
	require.NoError(t, err)
	return created
}
