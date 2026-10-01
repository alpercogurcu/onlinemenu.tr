package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/storefront/domain"
)

// BranchSettingsRepo manages storefront_branch_settings persistence.
type BranchSettingsRepo struct{}

func NewBranchSettingsRepo() *BranchSettingsRepo { return &BranchSettingsRepo{} }

const branchSettingsColumns = `tenant_id, branch_id, ordering_enabled, updated_by, created_at, updated_at`

// GetByBranch returns the branch's settings row, or ErrNotFound when none
// exists yet. Callers translate ErrNotFound into the defaults — the repo does
// not, so "a row exists" stays observable where the service needs it.
func (r *BranchSettingsRepo) GetByBranch(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) (domain.BranchSettings, error) {
	const q = `SELECT ` + branchSettingsColumns + ` FROM storefront_branch_settings WHERE branch_id = $1`

	s, err := scanBranchSettings(tx.QueryRow(ctx, q, branchID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.BranchSettings{}, ErrNotFound
		}
		return domain.BranchSettings{}, fmt.Errorf("storefront/repo/branch_settings: get by branch: %w", err)
	}
	return s, nil
}

// Upsert writes the branch's settings, creating the row on first change.
//
// The conflict target is the UNIQUE (branch_id) constraint; tenant agreement
// is not re-checked here because RLS already scopes both halves of the upsert
// to the transaction's tenant (tenant_isolation USING + WITH CHECK), and a
// branch id never moves between tenants.
func (r *BranchSettingsRepo) Upsert(ctx context.Context, tx pgx.Tx, s domain.BranchSettings) (domain.BranchSettings, error) {
	const q = `
		INSERT INTO storefront_branch_settings (tenant_id, branch_id, ordering_enabled, updated_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (branch_id) DO UPDATE
		SET ordering_enabled = EXCLUDED.ordering_enabled,
		    updated_by       = EXCLUDED.updated_by,
		    updated_at       = NOW()
		RETURNING ` + branchSettingsColumns

	saved, err := scanBranchSettings(tx.QueryRow(ctx, q,
		s.TenantID, s.BranchID, s.OrderingEnabled, s.UpdatedBy,
	))
	if err != nil {
		return domain.BranchSettings{}, fmt.Errorf("storefront/repo/branch_settings: upsert: %w", err)
	}
	return saved, nil
}

// scanBranchSettings reads one settings row from any RowScanner.
func scanBranchSettings(s interface {
	Scan(...any) error
}) (domain.BranchSettings, error) {
	var out domain.BranchSettings
	if err := s.Scan(
		&out.TenantID, &out.BranchID, &out.OrderingEnabled, &out.UpdatedBy, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return domain.BranchSettings{}, err
	}
	return out, nil
}
