package repo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/inventory/domain"
)

// SupplyPolicyRepo manages supply_policies persistence (ADR-DATA-007).
// There is deliberately no Update: policy changes are new rows with a later
// effective_from (DATA-002 immutability ruhu); see the migration comment.
type SupplyPolicyRepo struct{}

// NewSupplyPolicyRepo constructs a SupplyPolicyRepo for fx injection.
func NewSupplyPolicyRepo() *SupplyPolicyRepo { return &SupplyPolicyRepo{} }

// Create inserts a new supply policy row. The caller is responsible for
// generating policy.ID (client-side UUIDv7; mirrors stock_items' convention).
func (r *SupplyPolicyRepo) Create(ctx context.Context, tx pgx.Tx, policy domain.SupplyPolicy) (domain.SupplyPolicy, error) {
	approvedJSON, err := marshalApprovedSuppliers(policy.ApprovedSupplierIDs)
	if err != nil {
		return domain.SupplyPolicy{}, fmt.Errorf("inventory/repo/supply_policy: marshal approved_supplier_ids: %w", err)
	}

	const q = `
		INSERT INTO supply_policies (
			id, tenant_id, branch_id, scope, stock_item_id, category, mode,
			approved_supplier_ids, effective_from, created_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, tenant_id, branch_id, scope, stock_item_id, COALESCE(category, ''), mode,
			approved_supplier_ids, effective_from, created_by, created_at`

	row := tx.QueryRow(ctx, q,
		policy.ID, policy.TenantID, policy.BranchID, string(policy.Scope), policy.StockItemID,
		emptyToNil(policy.Category), string(policy.Mode), approvedJSON, policy.EffectiveFrom, policy.CreatedBy,
	)
	return scanSupplyPolicy(row)
}

// ListCandidates returns every supply policy row potentially relevant to
// resolving a stock item for a branch: all tenant-wide rows plus, when
// branchID is not uuid.Nil, all rows scoped to that branch. There is no
// "is active" filter here — domain.ResolvePolicy is the single place that
// picks the winner from the full candidate set (see migration 000006
// comment on why there is no partial index/flag for this).
func (r *SupplyPolicyRepo) ListCandidates(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) ([]domain.SupplyPolicy, error) {
	const q = `
		SELECT id, tenant_id, branch_id, scope, stock_item_id, COALESCE(category, ''), mode,
			approved_supplier_ids, effective_from, created_by, created_at
		FROM supply_policies
		WHERE branch_id IS NULL OR branch_id = $1`

	rows, err := tx.Query(ctx, q, branchID)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo/supply_policy: list candidates: %w", err)
	}
	defer rows.Close()

	var out []domain.SupplyPolicy
	for rows.Next() {
		p, err := scanSupplyPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo/supply_policy: list candidates scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListAll returns every supply policy row visible to the current RLS tenant
// context (all branches, all scopes) — used by the policy management
// listing endpoint.
func (r *SupplyPolicyRepo) ListAll(ctx context.Context, tx pgx.Tx) ([]domain.SupplyPolicy, error) {
	const q = `
		SELECT id, tenant_id, branch_id, scope, stock_item_id, COALESCE(category, ''), mode,
			approved_supplier_ids, effective_from, created_by, created_at
		FROM supply_policies
		ORDER BY effective_from DESC`

	rows, err := tx.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo/supply_policy: list all: %w", err)
	}
	defer rows.Close()

	var out []domain.SupplyPolicy
	for rows.Next() {
		p, err := scanSupplyPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo/supply_policy: list all scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// marshalApprovedSuppliers returns a value suitable for binding to a jsonb
// query parameter. It returns a string (not []byte): pgx's simple-protocol
// query exec mode (used by the test pool, see repo/integration_test.go)
// sends a []byte parameter as bytea, which Postgres then refuses to cast
// into jsonb ("invalid input syntax for type json") — a string parameter is
// sent as text and casts cleanly, mirroring tenant/repo's
// json.Marshal-then-string(...) convention (see branch_repo.go).
func marshalApprovedSuppliers(ids []uuid.UUID) (any, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func scanSupplyPolicy(s pgx.Row) (domain.SupplyPolicy, error) {
	var (
		p           domain.SupplyPolicy
		scope, mode string
		approvedRaw []byte
	)
	err := s.Scan(
		&p.ID, &p.TenantID, &p.BranchID, &scope, &p.StockItemID, &p.Category, &mode,
		&approvedRaw, &p.EffectiveFrom, &p.CreatedBy, &p.CreatedAt,
	)
	if err != nil {
		return domain.SupplyPolicy{}, err
	}
	p.Scope = domain.SupplyScope(scope)
	p.Mode = domain.SupplyMode(mode)
	if len(approvedRaw) > 0 {
		if err := json.Unmarshal(approvedRaw, &p.ApprovedSupplierIDs); err != nil {
			return domain.SupplyPolicy{}, fmt.Errorf("unmarshal approved_supplier_ids: %w", err)
		}
	}
	return p, nil
}
