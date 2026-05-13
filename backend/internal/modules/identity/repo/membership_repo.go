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

// ListContextsForPerson returns a ContextItem for every active membership the person holds
// across all tenants. This is a platform-level query: the caller must use a bootstrap tx
// (uuid.Nil tenant context) so that the persons RLS policy allows cross-tenant visibility.
func (r *MembershipRepo) ListContextsForPerson(ctx context.Context, tx pgx.Tx, personID uuid.UUID) ([]domain.ContextItem, error) {
	const q = `
		SELECT
			m.id,
			m.tenant_id,
			t.name,
			m.branch_id,
			COALESCE(b.name, ''),
			m.role_id,
			r.name
		FROM memberships m
		JOIN tenants  t ON t.id = m.tenant_id
		JOIN roles    r ON r.id = m.role_id
		LEFT JOIN branches b ON b.id = m.branch_id
		WHERE m.person_id = $1 AND m.status = 'active'
		ORDER BY t.name, COALESCE(b.name, ''), r.name`

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
