package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// BranchSettingsRepo manages pos_branch_settings persistence. It mirrors
// storefront's BranchSettingsRepo: a row is written lazily on the first
// change, and "no row" is reported as ErrNotFound so the service layer — not
// the repo — decides that the defaults apply.
type BranchSettingsRepo struct{}

func NewBranchSettingsRepo() *BranchSettingsRepo { return &BranchSettingsRepo{} }

const branchSettingsColumns = `tenant_id, branch_id, waiter_category_layout, order_flow, updated_by, created_at, updated_at`

// GetByBranch returns the branch's settings row, or ErrNotFound when none
// exists yet. Callers translate ErrNotFound into the defaults.
func (r *BranchSettingsRepo) GetByBranch(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) (domain.BranchSettings, error) {
	const q = `SELECT ` + branchSettingsColumns + ` FROM pos_branch_settings WHERE branch_id = $1`

	s, err := scanBranchSettings(tx.QueryRow(ctx, q, branchID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.BranchSettings{}, ErrNotFound
		}
		return domain.BranchSettings{}, fmt.Errorf("pos/repo/branch_settings: get by branch: %w", err)
	}
	return s, nil
}

// OrderFlowByBranch is the narrow read the order/check write paths take
// INSIDE their own transaction (no process-level cache, the same per-call
// discipline storefront's ordering gate follows): it resolves straight to the
// effective flow, defaulting to domain.OrderFlowFull when the branch has no
// row, so a caller can never observe "row missing" as anything but today's
// behaviour.
func (r *BranchSettingsRepo) OrderFlowByBranch(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) (domain.OrderFlow, error) {
	const q = `SELECT order_flow FROM pos_branch_settings WHERE branch_id = $1`

	var flow string
	if err := tx.QueryRow(ctx, q, branchID).Scan(&flow); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.OrderFlowFull, nil
		}
		return "", fmt.Errorf("pos/repo/branch_settings: order flow by branch: %w", err)
	}
	return domain.OrderFlow(flow), nil
}

// BranchSettingsPatch is Upsert's input: nil means "field not supplied, keep
// the row's current value — or the default when the upsert creates the row",
// matching ZonePatch/TablePatch's pointer-field convention.
type BranchSettingsPatch struct {
	TenantID             uuid.UUID
	BranchID             uuid.UUID
	WaiterCategoryLayout *domain.WaiterCategoryLayout
	OrderFlow            *domain.OrderFlow
	UpdatedBy            uuid.UUID
}

// Upsert writes the branch's settings, creating the row on first change. An
// omitted (nil) field keeps the existing value on conflict and falls back to
// domain.DefaultBranchSettings' value on insert — resolved via COALESCE in one
// statement, so two concurrent partial PUTs can never interleave a
// read-modify-write.
//
// The conflict target is the UNIQUE (branch_id) constraint; tenant agreement
// is not re-checked because RLS already scopes both halves of the upsert to
// the transaction's tenant, and a branch id never moves between tenants.
func (r *BranchSettingsRepo) Upsert(ctx context.Context, tx pgx.Tx, p BranchSettingsPatch) (domain.BranchSettings, error) {
	defaults := domain.DefaultBranchSettings(p.TenantID, p.BranchID)
	q := `
		INSERT INTO pos_branch_settings (tenant_id, branch_id, waiter_category_layout, order_flow, updated_by)
		VALUES ($1, $2,
		        COALESCE($3, '` + string(defaults.WaiterCategoryLayout) + `'),
		        COALESCE($4, '` + string(defaults.OrderFlow) + `'),
		        $5)
		ON CONFLICT (branch_id) DO UPDATE
		SET waiter_category_layout = COALESCE($3, pos_branch_settings.waiter_category_layout),
		    order_flow             = COALESCE($4, pos_branch_settings.order_flow),
		    updated_by             = $5,
		    updated_at             = NOW()
		RETURNING ` + branchSettingsColumns

	saved, err := scanBranchSettings(tx.QueryRow(ctx, q,
		p.TenantID, p.BranchID, layoutParam(p.WaiterCategoryLayout), flowParam(p.OrderFlow), p.UpdatedBy,
	))
	if err != nil {
		return domain.BranchSettings{}, fmt.Errorf("pos/repo/branch_settings: upsert: %w", err)
	}
	return saved, nil
}

// layoutParam / flowParam render the typed pointers as nullable text
// parameters: the pools run under pgx.QueryExecModeSimpleProtocol
// (ADR-SEC-001/002), which cannot encode a *domain.X named string type, so
// the value travels as *string exactly like uuidStrings' []string treatment.
func layoutParam(l *domain.WaiterCategoryLayout) *string {
	if l == nil {
		return nil
	}
	s := string(*l)
	return &s
}

func flowParam(f *domain.OrderFlow) *string {
	if f == nil {
		return nil
	}
	s := string(*f)
	return &s
}

// scanBranchSettings reads one settings row from any RowScanner.
func scanBranchSettings(s interface {
	Scan(...any) error
}) (domain.BranchSettings, error) {
	var out domain.BranchSettings
	var layout, flow string
	if err := s.Scan(
		&out.TenantID, &out.BranchID, &layout, &flow, &out.UpdatedBy, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return domain.BranchSettings{}, err
	}
	out.WaiterCategoryLayout = domain.WaiterCategoryLayout(layout)
	out.OrderFlow = domain.OrderFlow(flow)
	return out, nil
}
