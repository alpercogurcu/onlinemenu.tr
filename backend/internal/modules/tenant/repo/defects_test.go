package repo_test

// This file documents CONFIRMED defects found while writing the invariant
// suite. See the accompanying report for full detail, reproduction, and
// severity. Fixed defects are marked FIXED below with a live regression test
// (t.Skip removed); the fix is not to weaken an assertion to make it pass.
//
// Root cause common to the three "AcceptsMismatchedTenantBranch" tests
// below: BranchRepo/DocumentRepo/HoursRepo/IntegratorRepo mutation methods
// that take a (tenantID, branchID) pair NEVER verified branchID actually
// belonged to tenantID before writing. RLS only checks that the ROW BEING
// WRITTEN carries tenant_id = the active app.tenant_id GUC — it has no way
// to know, and did not check, whether branch_id references a branch that
// itself belongs to a different tenant (branches.tenant_id was not
// consulted at all). Nothing in the schema enforced it either:
// branch_regular_hours, branch_special_hours and
// branch_documents/billing_integrators.branch_id were plain FKs to
// branches(id) — "does this branch exist", not "does this branch belong to
// tenant_id on this row". This is exactly the b2b "child entity id mutation
// not re-checked" class from docs/lessons-from-b2b.md item 2, just with the
// roles reversed: instead of a stolen/reused id silently reading another
// tenant's row, it let a caller silently WRITE a row that references
// another tenant's real branch.
//
// FIXED (migration tenant/000006_branch_composite_fk): branches now carries
// a UNIQUE (id, tenant_id), and branch_documents.branch_id,
// billing_integrators.branch_id, branch_regular_hours.branch_id and
// branch_special_hours.branch_id are now composite FOREIGN KEY (branch_id,
// tenant_id) REFERENCES branches (id, tenant_id) — Postgres itself now
// rejects a caller's INSERT/UPDATE if the (branch_id, tenant_id) pair on the
// row being written doesn't match a real branch, closing the class at the
// schema layer regardless of which repo method or future call site writes
// the row. branch_regular_hours_unique and branch_special_hours_unique were
// additionally widened to include tenant_id, both for defense in depth and
// because branch_id alone already pins a single tenant once the FK holds.
// The HTTP-layer half of this defect (branchAccessMiddleware failing OPEN
// for chain-wide principals) is fixed separately in
// tenant/http/handler.go — see the report.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pub "onlinemenu.tr/internal/modules/tenant/public"
	"onlinemenu.tr/internal/modules/tenant/repo"
)

// TestDocumentRepo_Defect_BranchDocumentAcceptsMismatchedTenantBranch:
// tenant B, entirely within its own valid RLS context, creates a
// branch_documents row whose branch_id is tenant A's REAL branch. This
// currently SUCCEEDS (confirmed empirically) — see document_repo.go's
// CreateBranchDocument, which never looks up branchID's real owner.
//
// HTTP-reachability: DocumentRepo.CreateBranchDocument is called from
// http/handler.go's CreateBranchDocument via
// POST /tenants/{tenantID}/branches/{branchID}/documents. tenantAccessMiddleware
// only checks the JWT's own tenant == path tenantID; branchAccessMiddleware
// (http/handler.go branchAccessMiddleware) only checks
// principal.HasBranchAccess(branchID), which for a chain-wide principal
// (BranchID == uuid.Nil, platform/auth/principal.go HasBranchAccess) returns
// true for ANY branch id, including one from a different tenant — it never
// checks the branch's actual tenant. So a legitimate manager of tenant B who
// learns tenant A's real branch UUID (logs, another leaked reference, brute
// force of a v4 UUID space if ever exposed) can attach documents/hours/
// integrators to tenant A's branch that are tagged as tenant B's own data.
func TestDocumentRepo_Defect_BranchDocumentAcceptsMismatchedTenantBranch(t *testing.T) {
	// FIXED: branch_documents.branch_id is now a composite FK (branch_id,
	// tenant_id) REFERENCES branches(id, tenant_id) — see file-level comment
	// and migration tenant/000006_branch_composite_fk. Live regression test.
	ctx := context.Background()
	r := repo.NewDocumentRepo()

	tenantA := createTenant(t, ctx)
	branchA := createBranch(t, ctx, tenantA.ID)
	tenantB := createTenant(t, ctx)

	err := sharedPool.WithTenantTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		_, err := r.CreateBranchDocument(ctx, tx, pub.BranchDocument{
			TenantID:     tenantB.ID,
			BranchID:     branchA.ID, // tenant A's real branch
			DocumentType: pub.BranchDocKiraSozlesmesi,
			FileKey:      "should-be-rejected.pdf",
			FileName:     "should-be-rejected.pdf",
			FileSize:     1,
			MimeType:     "application/pdf",
			Status:       pub.DocStatusPending,
		})
		return err
	})
	require.Error(t, err, "creating a branch document for a branch owned by a different tenant must be rejected")
}

// TestHoursRepo_Defect_SetRegularHoursAcceptsMismatchedTenantBranch: same
// root cause as above, but for branch_regular_hours — and with a concrete,
// more severe consequence than an orphan row: branch_regular_hours_unique is
// UNIQUE (branch_id, day_of_week, sort_order), NOT scoped by tenant_id.
// Confirmed empirically: tenant B can "squat" an empty (day_of_week,
// sort_order) slot for tenant A's real branch BEFORE tenant A ever uses it;
// when tenant A later calls SetRegularHours for that same slot, the INSERT
// collides with tenant B's row and fails with an unhandled Postgres
// unique_violation, which service.SetRegularHours wraps as a generic
// "service: set regular hours: %w" and the HTTP handler's
// handleServiceErr (http/handler.go) falls through to its `default` branch
// — a 500 "internal error" with no indication of the cause. This is a
// denial-of-service on the branch's own opening-hours configuration, not
// merely a data-integrity nuisance.
func TestHoursRepo_Defect_SetRegularHoursAcceptsMismatchedTenantBranch(t *testing.T) {
	// FIXED: branch_regular_hours.branch_id is now a composite FK (branch_id,
	// tenant_id) REFERENCES branches(id, tenant_id), so tenant B's INSERT for
	// branchA.ID now fails outright — no slot to squat, so tenant A's own
	// later write can never be blocked by it. branch_regular_hours_unique was
	// also widened to (tenant_id, branch_id, day_of_week, sort_order). See
	// file-level comment and migration tenant/000006_branch_composite_fk.
	// Live regression test.
	ctx := context.Background()
	hr := repo.NewHoursRepo()

	tenantA := createTenant(t, ctx)
	branchA := createBranch(t, ctx, tenantA.ID)
	tenantB := createTenant(t, ctx)

	// Ideal behaviour: this write must be rejected outright because branchA
	// does not belong to tenantB.
	err := sharedPool.WithTenantTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		return hr.SetRegularHours(ctx, tx, tenantB.ID, branchA.ID, []pub.RegularHours{
			{TenantID: tenantB.ID, BranchID: branchA.ID, DayOfWeek: time.Monday, IsClosed: true},
		})
	})
	require.Error(t, err, "setting hours for a branch owned by a different tenant must be rejected")

	// Even if the above were somehow allowed, tenant A's own subsequent
	// write to the same slot must never be blocked by it.
	err = sharedPool.WithTenantTx(ctx, tenantA.ID, func(tx pgx.Tx) error {
		return hr.SetRegularHours(ctx, tx, tenantA.ID, branchA.ID, []pub.RegularHours{
			{TenantID: tenantA.ID, BranchID: branchA.ID, DayOfWeek: time.Monday, OpenTime: tod(9, 0), CloseTime: tod(18, 0)},
		})
	})
	assert.NoError(t, err, "tenant A's legitimate write to its own branch must never be blockable by another tenant")
}

// TestIntegratorRepo_Defect_CreateIntegratorAcceptsMismatchedTenantBranch:
// same root cause, for billing_integrators.branch_id.
func TestIntegratorRepo_Defect_CreateIntegratorAcceptsMismatchedTenantBranch(t *testing.T) {
	// FIXED: billing_integrators.branch_id is now a composite FK (branch_id,
	// tenant_id) REFERENCES branches(id, tenant_id), MATCH SIMPLE (the
	// default): a NULL branch_id (tenant-wide integrator) still skips the
	// check entirely, exactly as before — only a NON-NULL, mismatched
	// branch_id is now rejected. See file-level comment and migration
	// tenant/000006_branch_composite_fk. Live regression test.
	ctx := context.Background()
	r := repo.NewIntegratorRepo()

	tenantA := createTenant(t, ctx)
	branchA := createBranch(t, ctx, tenantA.ID)
	tenantB := createTenant(t, ctx)

	err := sharedPool.WithTenantTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		_, err := r.CreateIntegrator(ctx, tx, pub.BillingIntegrator{
			TenantID: tenantB.ID, BranchID: &branchA.ID, Provider: pub.ProviderEDM,
			DisplayName: "should-be-rejected", Config: map[string]any{}, Environment: pub.EnvTest, IsActive: true,
		})
		return err
	})
	require.Error(t, err, "creating an integrator for a branch owned by a different tenant must be rejected")
}

// TestService_Defect_DocumentStatus_NoTransitionGuard: docs/lessons-from-b2b.md's
// status-transition lesson, applied here. service.UpdateDocumentStatus (and
// UpdateBranchDocumentStatus) validate only that the TARGET status is one of
// the four enum values (validDocumentStatus in service/service.go) — there
// is no allowedTransitions map and no single Transition() chokepoint. This
// means, confirmed empirically:
//   - verified -> pending (backward) succeeds
//   - rejected -> verified (skipping re-review) succeeds
//   - pending -> rejected with an EMPTY rejection_note succeeds, even though
//     document_repo.go's UpdateDocumentStatus column comment and the wider
//     domain expectation (a tenant needs to know WHY their tax document was
//     rejected) implies a note should be mandatory for that transition.
//
// This exercises DocumentRepo.UpdateDocumentStatus directly rather than
// going through service.Service: go-arch-lint forbids the repo package
// (including its tests) from importing service (service depends on repo,
// not the reverse — internal/modules/tenant/repo/defects_test.go would
// otherwise be a reverse dependency), and repo is in fact where the
// guarantee needs to hold — service.validDocumentStatus (service/service.go)
// only re-checks that the target status is one of the four enum values, it
// adds no transition logic on top of what's shown failing here.
func TestDocumentRepo_Defect_UpdateDocumentStatus_NoTransitionGuard(t *testing.T) {
	// FIXED: DocumentRepo.UpdateDocumentStatus now routes through
	// validateTransition against the allowedDocumentTransitions map
	// (document_repo.go) — backward and skip-ahead transitions are rejected,
	// and any transition into "rejected" requires a non-empty
	// rejection_note. Live regression test.
	ctx := context.Background()
	r := repo.NewDocumentRepo()
	tenant := createTenant(t, ctx)
	doc := createDocument(t, ctx, tenant.ID)

	setStatus := func(status pub.DocumentStatus, note string) error {
		return sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
			return r.UpdateDocumentStatus(ctx, tx, tenant.ID, doc.ID, status, note)
		})
	}

	require.NoError(t, setStatus(pub.DocStatusVerified, ""))

	err := setStatus(pub.DocStatusPending, "")
	assert.Error(t, err, "verified -> pending must be rejected: there is no legitimate reason to revert an approved document to pending")

	err = setStatus(pub.DocStatusRejected, "")
	assert.Error(t, err, "transitioning to rejected without a rejection_note must be rejected")
}

// TestTenantRepo_Defect_SecondTenantWithoutTaxNo_Collides: tenants.tax_no and
// tenants.mersis_no both carry a partial UNIQUE index (`WHERE ... IS NOT
// NULL`), explicitly to allow multiple tenants to leave the field unset
// during onboarding (migration 000002's comment: "NULL izni: mevcut
// tenant'ların migration sırasında verileri eksik olabilir; uygulama
// katmanında onboarding tamamlanmadan aktif edilemez"). But
// public.Tenant.TaxNo / MersisNo are plain Go `string`, not `*string` or
// sql.NullString, and TenantRepo.Create (repo/tenant_repo.go) binds them
// directly with no empty-to-NULL conversion. An unset field therefore
// inserts SQL ” (empty string), which IS NOT NULL — so the SECOND tenant
// ever onboarded without a tax number collides with the first on
// tenants_tax_no_idx (confirmed empirically: real Postgres 23505
// duplicate-key error). The intended "leave it blank during onboarding"
// path is broken for every tenant after the first.
func TestTenantRepo_Defect_SecondTenantWithoutTaxNo_Collides(t *testing.T) {
	// FIXED: TenantRepo now writes NULLIF($n, '') for tax_no/mersis_no and
	// reads them back with COALESCE(col, ''), so an unset field lands as NULL
	// and the partial unique indexes ignore it — which is what migration
	// 000002's own comment ("onboarding kolaylığı") always intended. This is
	// now a live regression test; do not re-skip it.
	ctx := context.Background()
	r := repo.NewTenantRepo()

	mk := func(slug string) error {
		id := mustNewID(t)
		return sharedPool.WithTenantTx(ctx, id, func(tx pgx.Tx) error {
			_, err := r.Create(ctx, tx, pub.Tenant{
				ID: id, Name: "Onboarding " + slug, Slug: slug,
				Plan: pub.PlanStarter, EnabledModules: []string{"pos"},
				IdentityType: pub.IdentityKurumsal, IsActive: true,
				// TaxNo / MersisNo deliberately left unset, as a real
				// still-onboarding tenant would.
			})
			return err
		})
	}

	require.NoError(t, mk("onboarding-1-"+uniqueSuffix()))
	require.NoError(t, mk("onboarding-2-"+uniqueSuffix()),
		"a second tenant with no tax number yet must be able to onboard")
}

// TestBranchRepo_Defect_SecondBranchWithoutTaxNo_Collides: the same defect
// class as above, for branches.tax_no (branches_tax_no_idx, migration
// tenant/000003) — and notably this index is GLOBAL, not scoped per tenant,
// so it collides across UNRELATED tenants' branches, not just within one.
func TestBranchRepo_Defect_SecondBranchWithoutTaxNo_Collides(t *testing.T) {
	// FIXED: BranchRepo now writes NULLIF($n, '') for tax_no; the read side
	// already used COALESCE. Live regression test — do not re-skip.
	ctx := context.Background()
	r := repo.NewBranchRepo()

	mk := func(slug string) error {
		tenant := createTenant(t, ctx)
		return sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
			_, err := r.CreateBranch(ctx, tx, pub.Branch{
				TenantID: tenant.ID, Name: "Şube " + slug, Slug: slug,
				OwnershipType: pub.OwnershipSube, OperationType: pub.OperationRestoran,
				IdentityType: pub.IdentityKurumsal, IsActive: true,
				// TaxNo deliberately left unset (directly-operated branch,
				// per the OwnershipSube/franchise distinction in public/tenant.go).
			})
			return err
		})
	}

	require.NoError(t, mk("branch-1-"+uniqueSuffix()))
	require.NoError(t, mk("branch-2-"+uniqueSuffix()),
		"a second directly-operated branch with no tax number must be able to onboard")
}

// TestTenantRepo_Defect_NullableTextColumnsCrashScan: a tenant seeded with
// only the NOT NULL columns (id, name, slug, plan — exactly how a real
// pre-onboarding-completion tenant looks, per migration 000002's own
// comment: "NULL izni: mevcut tenant'ların migration sırasında verileri
// eksik olabilir") leaves legal_name, trade_name, tax_office, address, city,
// district, postal_code, phone and contact_email NULL. TenantRepo.GetByID's
// SELECT only COALESCEd tax_no/mersis_no and scanTenant scanned the rest
// into plain Go strings — confirmed empirically: GetByID returned "can't
// scan into dest[2]: cannot scan NULL into *string", a 500 on every read of
// such a tenant.
func TestTenantRepo_Defect_NullableTextColumnsCrashScan(t *testing.T) {
	// FIXED: every nullable text column feeding scanTenant is now COALESCEd
	// to '' in GetByID/Create/Update's SELECT/RETURNING clauses
	// (repo/tenant_repo.go), the same approach already used for
	// tax_no/mersis_no. Live regression test — do not re-skip.
	ctx := context.Background()
	r := repo.NewTenantRepo()

	id := mustNewID(t)
	slug := "bare-" + uniqueSuffix()
	err := sharedPool.WithTenantTx(ctx, id, func(tx pgx.Tx) error {
		const insert = `INSERT INTO tenants (id, name, slug, plan) VALUES ($1, $2, $3, $4)`
		_, err := tx.Exec(ctx, insert, id, "Bare Tenant "+slug, slug, "starter")
		return err
	})
	require.NoError(t, err, "seed insert with only NOT NULL columns must succeed")

	err = sharedPool.WithTenantReadTx(ctx, id, func(tx pgx.Tx) error {
		got, err := r.GetByID(ctx, tx, id)
		if err != nil {
			return err
		}
		assert.Equal(t, "", got.LegalName)
		assert.Equal(t, "", got.TradeName)
		assert.Equal(t, "", got.TaxNo)
		assert.Equal(t, "", got.TaxOffice)
		assert.Equal(t, "", got.MersisNo)
		assert.Equal(t, "", got.Address.Line1)
		assert.Equal(t, "", got.Address.City)
		assert.Equal(t, "", got.Address.District)
		assert.Equal(t, "", got.Address.PostalCode)
		assert.Equal(t, "TR", got.Address.Country, "country has a NOT NULL DEFAULT 'TR'")
		assert.Equal(t, "", got.Phone)
		assert.Equal(t, "", got.ContactEmail)
		return nil
	})
	require.NoError(t, err, "GetByID must not fail scanning a tenant with NULL optional text columns")
}
