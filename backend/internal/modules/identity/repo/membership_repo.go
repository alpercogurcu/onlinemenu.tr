package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/identity/domain"
	pub "onlinemenu.tr/internal/modules/identity/public"
)

// MembershipRepo provides data access for the memberships table.
type MembershipRepo struct{}

// NewMembershipRepo constructs a MembershipRepo for fx injection.
func NewMembershipRepo() *MembershipRepo {
	return &MembershipRepo{}
}

// GetByID fetches a single membership scoped to the given tenant.
func (r *MembershipRepo) GetByID(ctx context.Context, tx pgx.Tx, tenantID, membershipID uuid.UUID) (domain.Membership, error) {
	const q = `
		SELECT id, person_id, tenant_id, branch_id, role_id, status, created_at, updated_at
		FROM memberships
		WHERE id = $1 AND tenant_id = $2`

	row := tx.QueryRow(ctx, q, membershipID, tenantID)
	m, err := scanMembership(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Membership{}, pub.ErrNotFound
		}
		return domain.Membership{}, fmt.Errorf("identity/repo/membership: get by id: %w", err)
	}
	return m, nil
}

// CountChainWideForRole counts the memberships that grant roleID across the whole
// chain (branch_id IS NULL) and are still capable of conferring permissions.
//
// 'terminated' rows are excluded deliberately: they are history, never resolved
// by ActiveRoleIDsAt, and counting them would block a legitimate branch_scoped
// flip forever with no action the admin could take short of deleting audit rows.
// 'suspended' rows are counted — suspension is reversible, so leaving one behind
// only defers the leak until the membership is reactivated.
func (r *MembershipRepo) CountChainWideForRole(ctx context.Context, tx pgx.Tx, tenantID, roleID uuid.UUID) (int, error) {
	const q = `
		SELECT COUNT(*)
		FROM memberships
		WHERE tenant_id = $1
		  AND role_id = $2
		  AND branch_id IS NULL
		  AND status <> 'terminated'`

	var count int
	if err := tx.QueryRow(ctx, q, tenantID, roleID).Scan(&count); err != nil {
		return 0, fmt.Errorf("identity/repo/membership: count chain-wide for role: %w", err)
	}
	return count, nil
}

// ListByPerson returns all active memberships for a person within a tenant.
func (r *MembershipRepo) ListByPerson(ctx context.Context, tx pgx.Tx, tenantID, personID uuid.UUID) ([]domain.Membership, error) {
	const q = `
		SELECT id, person_id, tenant_id, branch_id, role_id, status, created_at, updated_at
		FROM memberships
		WHERE tenant_id = $1 AND person_id = $2 AND status = 'active'
		ORDER BY created_at`

	rows, err := tx.Query(ctx, q, tenantID, personID)
	if err != nil {
		return nil, fmt.Errorf("identity/repo/membership: list by person: %w", err)
	}
	defer rows.Close()
	return collectMemberships(rows)
}

// ListForTenant returns all memberships within a tenant. When personID is non-nil
// the result is filtered to that person; when branchID is non-nil the result is
// further filtered to that branch (including chain-wide memberships with nil branch_id).
func (r *MembershipRepo) ListForTenant(
	ctx context.Context,
	tx pgx.Tx,
	tenantID uuid.UUID,
	personID *uuid.UUID,
	branchID *uuid.UUID,
) ([]domain.Membership, error) {
	const base = `
		SELECT id, person_id, tenant_id, branch_id, role_id, status, created_at, updated_at
		FROM memberships
		WHERE tenant_id = $1`

	args := []any{tenantID}
	q := base
	if personID != nil {
		args = append(args, *personID)
		q += fmt.Sprintf(" AND person_id = $%d", len(args))
	}
	if branchID != nil {
		args = append(args, *branchID)
		q += fmt.Sprintf(" AND (branch_id = $%d OR branch_id IS NULL)", len(args))
	}
	q += " ORDER BY created_at"

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("identity/repo/membership: list for tenant: %w", err)
	}
	defer rows.Close()
	return collectMemberships(rows)
}

// ListDetailsForTenant is ListForTenant joined with persons and roles for the
// admin user list. LEFT JOINs (not INNER) so a membership never disappears
// because its person/role row is invisible under the current RLS context —
// persons_select only exposes people who hold a membership in this tenant,
// which every listed row satisfies, but the join must not turn a policy
// change into silently missing users.
func (r *MembershipRepo) ListDetailsForTenant(
	ctx context.Context,
	tx pgx.Tx,
	tenantID uuid.UUID,
	personID *uuid.UUID,
	branchID *uuid.UUID,
) ([]domain.MembershipDetail, error) {
	const base = `
		SELECT m.id, m.person_id, m.tenant_id, m.branch_id, m.role_id, m.status, m.created_at, m.updated_at,
		       COALESCE(p.full_name, ''), COALESCE(p.email, ''), COALESCE(r.name, '')
		FROM memberships m
		LEFT JOIN persons p ON p.id = m.person_id
		LEFT JOIN roles r ON r.id = m.role_id
		WHERE m.tenant_id = $1`

	args := []any{tenantID}
	q := base
	if personID != nil {
		args = append(args, *personID)
		q += fmt.Sprintf(" AND m.person_id = $%d", len(args))
	}
	if branchID != nil {
		args = append(args, *branchID)
		q += fmt.Sprintf(" AND (m.branch_id = $%d OR m.branch_id IS NULL)", len(args))
	}
	q += " ORDER BY m.created_at"

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("identity/repo/membership: list details for tenant: %w", err)
	}
	defer rows.Close()

	var details []domain.MembershipDetail
	for rows.Next() {
		var (
			d         domain.MembershipDetail
			status    string
			createdAt time.Time
			updatedAt time.Time
		)
		if err := rows.Scan(
			&d.ID, &d.PersonID, &d.TenantID, &d.BranchID,
			&d.RoleID, &status, &createdAt, &updatedAt,
			&d.PersonName, &d.PersonEmail, &d.RoleName,
		); err != nil {
			return nil, fmt.Errorf("identity/repo/membership: scan detail: %w", err)
		}
		d.Status = domain.MembershipStatus(status)
		d.CreatedAt = createdAt
		d.UpdatedAt = updatedAt
		details = append(details, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity/repo/membership: detail rows: %w", err)
	}
	return details, nil
}

// ListContextsForPerson returns a ContextItem for every active membership the person holds
// across all tenants. This is a platform-level query: the caller must use
// db.WithAllTenantsTx/WithAllTenantsReadTx (app.tenant_scope = 'all_tenants')
// so that the memberships RLS policy allows cross-tenant visibility.
//
// tenants and roles are LEFT JOINed rather than INNER JOINed on purpose: the
// tenants table (owned by the tenant module) and tenant/branch-scoped custom
// roles have their own RLS policies keyed on app.tenant_id, which is NOT set
// under WithAllTenantsReadTx (only app.tenant_scope is). An INNER JOIN would
// silently drop every cross-tenant membership row whose tenant/role name
// happens to be invisible under the caller's current RLS context, which
// defeats the whole point of a cross-tenant listing. TenantName/RoleName may
// come back empty for a tenant/role the caller cannot otherwise see; callers
// must not treat an empty name as an error. MembershipID/TenantID/BranchID/
// RoleID (the fields actually used by ContextService.SelectContext to resolve
// which tenant+branch to issue a token for) always come from the memberships
// row itself and are never affected by this.
func (r *MembershipRepo) ListContextsForPerson(ctx context.Context, tx pgx.Tx, personID uuid.UUID) ([]domain.ContextItem, error) {
	const q = `
		SELECT
			m.id,
			m.tenant_id,
			COALESCE(t.name, ''),
			m.branch_id,
			COALESCE(b.name, ''),
			m.role_id,
			COALESCE(r.name, '')
		FROM memberships m
		LEFT JOIN tenants  t ON t.id = m.tenant_id
		LEFT JOIN roles    r ON r.id = m.role_id
		LEFT JOIN branches b ON b.id = m.branch_id
		WHERE m.person_id = $1 AND m.status = 'active'
		ORDER BY COALESCE(t.name, ''), COALESCE(b.name, ''), COALESCE(r.name, '')`

	rows, err := tx.Query(ctx, q, personID)
	if err != nil {
		return nil, fmt.Errorf("identity/repo/membership: list contexts for person: %w", err)
	}
	defer rows.Close()

	var items []domain.ContextItem
	for rows.Next() {
		item, err := scanContextItem(rows)
		if err != nil {
			return nil, fmt.Errorf("identity/repo/membership: scan context item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity/repo/membership: list contexts rows: %w", err)
	}
	return items, nil
}

// Create inserts a new membership and returns the persisted record.
func (r *MembershipRepo) Create(ctx context.Context, tx pgx.Tx, m domain.Membership) (domain.Membership, error) {
	const q = `
		INSERT INTO memberships (person_id, tenant_id, branch_id, role_id, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, person_id, tenant_id, branch_id, role_id, status, created_at, updated_at`

	row := tx.QueryRow(ctx, q, m.PersonID, m.TenantID, m.BranchID, m.RoleID, string(m.Status))
	created, err := scanMembership(row)
	if err != nil {
		return domain.Membership{}, fmt.Errorf("identity/repo/membership: create: %w", err)
	}
	return created, nil
}

// FindOrCreate returns the existing membership matching
// (person_id, tenant_id, branch_id, role_id), or creates one from m if none
// exists yet.
//
// This is the idempotency primitive the staff invite flow (ADR-AUTH-003)
// relies on for its final write: a retried invite for a person who already
// holds this exact membership must succeed by returning the existing row,
// not by raising a unique-violation the caller has to specially handle. A
// person can legitimately hold the SAME role at a DIFFERENT branch, or a
// DIFFERENT role at the same branch — only an exact quadruple match is
// treated as "already granted"; anything else falls through to a normal
// insert.
//
// ON CONFLICT ON CONSTRAINT memberships_unique DO NOTHING is used rather than
// catching the unique-violation error for the same reason as
// PersonRepo.FindOrCreateByKeycloakSub: a raised error aborts the
// transaction, and the fallback SELECT needs to run inside it. The
// constraint uses NULLS NOT DISTINCT (identity migration 000012), so a
// second chain-wide grant (branch_id IS NULL) for the same person+role
// conflicts correctly instead of silently duplicating.
func (r *MembershipRepo) FindOrCreate(ctx context.Context, tx pgx.Tx, m domain.Membership) (domain.Membership, error) {
	const insertQ = `
		INSERT INTO memberships (person_id, tenant_id, branch_id, role_id, status)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT ON CONSTRAINT memberships_unique DO NOTHING
		RETURNING id, person_id, tenant_id, branch_id, role_id, status, created_at, updated_at`

	row := tx.QueryRow(ctx, insertQ, m.PersonID, m.TenantID, m.BranchID, m.RoleID, string(m.Status))
	created, err := scanMembership(row)
	if err == nil {
		return created, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, fmt.Errorf("identity/repo/membership: find or create: insert: %w", err)
	}

	const selectQ = `
		SELECT id, person_id, tenant_id, branch_id, role_id, status, created_at, updated_at
		FROM memberships
		WHERE person_id = $1 AND tenant_id = $2
		  AND branch_id IS NOT DISTINCT FROM $3
		  AND role_id = $4`

	existing, err := scanMembership(tx.QueryRow(ctx, selectQ, m.PersonID, m.TenantID, m.BranchID, m.RoleID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Membership{}, pub.ErrNotFound
		}
		return domain.Membership{}, fmt.Errorf("identity/repo/membership: find or create: refetch: %w", err)
	}
	return existing, nil
}

// UpdateStatus changes the lifecycle status of a membership.
func (r *MembershipRepo) UpdateStatus(ctx context.Context, tx pgx.Tx, tenantID, membershipID uuid.UUID, status domain.MembershipStatus) error {
	const q = `
		UPDATE memberships
		SET status = $1, updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3`

	ct, err := tx.Exec(ctx, q, string(status), membershipID, tenantID)
	if err != nil {
		return fmt.Errorf("identity/repo/membership: update status: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pub.ErrNotFound
	}
	return nil
}

// ActiveRoleIDsAt returns the role IDs for all active memberships a person holds at the
// given branch within the tenant. Chain-wide memberships (branch_id IS NULL) are included
// because they apply to every branch in the tenant.
func (r *MembershipRepo) ActiveRoleIDsAt(ctx context.Context, tx pgx.Tx, tenantID, personID, branchID uuid.UUID) ([]uuid.UUID, error) {
	const q = `
		SELECT role_id
		FROM memberships
		WHERE tenant_id = $1
		  AND person_id = $2
		  AND status = 'active'
		  AND (branch_id = $3 OR branch_id IS NULL)`

	rows, err := tx.Query(ctx, q, tenantID, personID, branchID)
	if err != nil {
		return nil, fmt.Errorf("identity/repo/membership: active role ids at: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("identity/repo/membership: scan role id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity/repo/membership: active role ids rows: %w", err)
	}
	return ids, nil
}

func scanMembership(row pgx.Row) (domain.Membership, error) {
	var (
		m         domain.Membership
		status    string
		createdAt time.Time
		updatedAt time.Time
	)
	err := row.Scan(
		&m.ID, &m.PersonID, &m.TenantID, &m.BranchID,
		&m.RoleID, &status, &createdAt, &updatedAt,
	)
	if err != nil {
		return domain.Membership{}, err
	}
	m.Status = domain.MembershipStatus(status)
	m.CreatedAt = createdAt
	m.UpdatedAt = updatedAt
	return m, nil
}

func scanContextItem(rows pgx.Rows) (domain.ContextItem, error) {
	var item domain.ContextItem
	err := rows.Scan(
		&item.MembershipID,
		&item.TenantID, &item.TenantName,
		&item.BranchID, &item.BranchName,
		&item.RoleID, &item.RoleName,
	)
	if err != nil {
		return domain.ContextItem{}, err
	}
	return item, nil
}

func collectMemberships(rows pgx.Rows) ([]domain.Membership, error) {
	var memberships []domain.Membership
	for rows.Next() {
		var (
			m         domain.Membership
			status    string
			createdAt time.Time
			updatedAt time.Time
		)
		if err := rows.Scan(
			&m.ID, &m.PersonID, &m.TenantID, &m.BranchID,
			&m.RoleID, &status, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("identity/repo/membership: scan: %w", err)
		}
		m.Status = domain.MembershipStatus(status)
		m.CreatedAt = createdAt
		m.UpdatedAt = updatedAt
		memberships = append(memberships, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity/repo/membership: rows: %w", err)
	}
	return memberships, nil
}
