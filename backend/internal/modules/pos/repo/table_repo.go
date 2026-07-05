package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// TableRepo manages table_zones and tables persistence.
type TableRepo struct{}

func NewTableRepo() *TableRepo { return &TableRepo{} }

// TableWithCheck pairs a table with the id of the check currently open
// against it (nil if the table has no open check). This is a query
// projection, not a persisted entity — it exists so the floor-plan list
// endpoint can render "table X is occupied by check Y" in a single request
// (Wave-1 goal: the cash register draws the whole plan from one call).
type TableWithCheck struct {
	Table domain.Table
	// ZoneName/ZoneFloor are denormalized from table_zones so the floor-plan
	// list endpoint can group/label by zone without a second request.
	ZoneName      string
	ZoneFloor     int
	ActiveCheckID *uuid.UUID
}

// ---------------------------------------------------------------------------
// Zones
// ---------------------------------------------------------------------------

// CreateZone inserts a new table zone.
func (r *TableRepo) CreateZone(ctx context.Context, tx pgx.Tx, z domain.TableZone) (domain.TableZone, error) {
	const q = `
		INSERT INTO table_zones (tenant_id, branch_id, name, floor, is_active)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, tenant_id, branch_id, name, floor, is_active, created_at, updated_at`

	row := tx.QueryRow(ctx, q, z.TenantID, z.BranchID, z.Name, z.Floor, z.IsActive)
	return scanZone(row)
}

// GetZoneByID returns a zone visible to the current tenant context.
func (r *TableRepo) GetZoneByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.TableZone, error) {
	const q = `
		SELECT id, tenant_id, branch_id, name, floor, is_active, created_at, updated_at
		FROM table_zones WHERE id = $1`

	z, err := scanZone(tx.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.TableZone{}, ErrNotFound
		}
		return domain.TableZone{}, fmt.Errorf("pos/repo/table: get zone by id: %w", err)
	}
	return z, nil
}

// ListZonesByBranch returns all zones for a branch, active first then by floor.
func (r *TableRepo) ListZonesByBranch(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) ([]domain.TableZone, error) {
	const q = `
		SELECT id, tenant_id, branch_id, name, floor, is_active, created_at, updated_at
		FROM table_zones
		WHERE branch_id = $1
		ORDER BY is_active DESC, floor, name`

	rows, err := tx.Query(ctx, q, branchID)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/table: list zones: %w", err)
	}
	defer rows.Close()

	var out []domain.TableZone
	for rows.Next() {
		z, err := scanZone(rows)
		if err != nil {
			return nil, fmt.Errorf("pos/repo/table: list zones scan: %w", err)
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

// UpdateZone updates a zone's mutable fields (name, floor, is_active).
func (r *TableRepo) UpdateZone(ctx context.Context, tx pgx.Tx, z domain.TableZone) (domain.TableZone, error) {
	const q = `
		UPDATE table_zones SET name = $2, floor = $3, is_active = $4, updated_at = NOW()
		WHERE id = $1
		RETURNING id, tenant_id, branch_id, name, floor, is_active, created_at, updated_at`

	updated, err := scanZone(tx.QueryRow(ctx, q, z.ID, z.Name, z.Floor, z.IsActive))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.TableZone{}, ErrNotFound
		}
		return domain.TableZone{}, fmt.Errorf("pos/repo/table: update zone: %w", err)
	}
	return updated, nil
}

func scanZone(s interface{ Scan(...any) error }) (domain.TableZone, error) {
	var z domain.TableZone
	if err := s.Scan(&z.ID, &z.TenantID, &z.BranchID, &z.Name, &z.Floor, &z.IsActive, &z.CreatedAt, &z.UpdatedAt); err != nil {
		return domain.TableZone{}, err
	}
	return z, nil
}

// ---------------------------------------------------------------------------
// Tables
// ---------------------------------------------------------------------------

// CreateTable inserts a new table, defaulting to "empty" status.
func (r *TableRepo) CreateTable(ctx context.Context, tx pgx.Tx, t domain.Table) (domain.Table, error) {
	layout := t.LayoutPosition
	if layout == nil {
		layout = json.RawMessage(`{}`)
	}
	const q = `
		INSERT INTO tables (tenant_id, branch_id, zone_id, name, capacity, status, layout_position, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, tenant_id, branch_id, zone_id, name, capacity, status,
		          layout_position, is_active, created_at, updated_at`

	// layout is passed as string (not []byte) so pgx sends it as text, which
	// Postgres coerces text -> JSONB (mirrors repo/outbox.go's InsertOutbox).
	row := tx.QueryRow(ctx, q,
		t.TenantID, t.BranchID, t.ZoneID, t.Name, t.Capacity, string(domain.TableStatusEmpty), string(layout), t.IsActive,
	)
	return scanTable(row)
}

// GetTableByID returns a table visible to the current tenant context.
func (r *TableRepo) GetTableByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Table, error) {
	const q = `
		SELECT id, tenant_id, branch_id, zone_id, name, capacity, status,
		       layout_position, is_active, created_at, updated_at
		FROM tables WHERE id = $1`

	t, err := scanTable(tx.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Table{}, ErrNotFound
		}
		return domain.Table{}, fmt.Errorf("pos/repo/table: get table by id: %w", err)
	}
	return t, nil
}

// GetTableForUpdate locks the table row for the duration of the caller's
// transaction. CheckService.Open uses this to serialize concurrent attempts
// to open a check against the same table: the second caller blocks here
// until the first commits (occupied) or rolls back, then observes the
// already-occupied status. The manual status-change endpoint
// (TableService.SetStatus) uses the same lock for the same reason.
func (r *TableRepo) GetTableForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Table, error) {
	const q = `
		SELECT id, tenant_id, branch_id, zone_id, name, capacity, status,
		       layout_position, is_active, created_at, updated_at
		FROM tables WHERE id = $1 FOR UPDATE`

	t, err := scanTable(tx.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Table{}, ErrNotFound
		}
		return domain.Table{}, fmt.Errorf("pos/repo/table: get table for update: %w", err)
	}
	return t, nil
}

// ListTablesByBranch returns every active table for a branch together with
// its zone's name/floor and the id of its currently open check (nil if
// none), ordered by (zone.floor, zone.name, zone.id, table.name) so the
// cash register can draw the whole floor plan — grouped by zone, in that
// order — from this single request (see service.TableService.ListTables /
// http.listTables, which group these rows into the zone-grouped response).
// z.id is included as a tiebreaker: without it, two zones sharing the same
// (floor, name) would interleave their tables in the t.name sort, breaking
// http.toZonePlanResponse's contiguity-based grouping (it would emit the
// same zone as two separate, non-adjacent sections in the response).
func (r *TableRepo) ListTablesByBranch(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) ([]TableWithCheck, error) {
	const q = `
		SELECT t.id, t.tenant_id, t.branch_id, t.zone_id, t.name, t.capacity, t.status,
		       t.layout_position, t.is_active, t.created_at, t.updated_at,
		       z.name, z.floor, c.id
		FROM tables t
		JOIN table_zones z ON z.id = t.zone_id
		LEFT JOIN checks c ON c.table_id = t.id AND c.status = 'open'
		WHERE t.branch_id = $1 AND t.is_active
		ORDER BY z.floor, z.name, z.id, t.name`

	rows, err := tx.Query(ctx, q, branchID)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/table: list tables: %w", err)
	}
	defer rows.Close()

	var out []TableWithCheck
	for rows.Next() {
		var (
			t             domain.Table
			status        string
			zoneName      string
			zoneFloor     int
			activeCheckID *uuid.UUID
		)
		if err := rows.Scan(
			&t.ID, &t.TenantID, &t.BranchID, &t.ZoneID, &t.Name, &t.Capacity, &status,
			&t.LayoutPosition, &t.IsActive, &t.CreatedAt, &t.UpdatedAt,
			&zoneName, &zoneFloor, &activeCheckID,
		); err != nil {
			return nil, fmt.Errorf("pos/repo/table: list tables scan: %w", err)
		}
		t.Status = domain.TableStatus(status)
		out = append(out, TableWithCheck{
			Table:         t,
			ZoneName:      zoneName,
			ZoneFloor:     zoneFloor,
			ActiveCheckID: activeCheckID,
		})
	}
	return out, rows.Err()
}

// UpdateTable updates a table's mutable descriptive fields (name, capacity,
// zone_id, layout_position, is_active). Status is never touched here — it
// only moves through UpdateStatus, guarded by the state machine.
func (r *TableRepo) UpdateTable(ctx context.Context, tx pgx.Tx, t domain.Table) (domain.Table, error) {
	layout := t.LayoutPosition
	if layout == nil {
		layout = json.RawMessage(`{}`)
	}
	const q = `
		UPDATE tables
		SET zone_id = $2, name = $3, capacity = $4, layout_position = $5, is_active = $6, updated_at = NOW()
		WHERE id = $1
		RETURNING id, tenant_id, branch_id, zone_id, name, capacity, status,
		          layout_position, is_active, created_at, updated_at`

	updated, err := scanTable(tx.QueryRow(ctx, q, t.ID, t.ZoneID, t.Name, t.Capacity, string(layout), t.IsActive))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Table{}, ErrNotFound
		}
		return domain.Table{}, fmt.Errorf("pos/repo/table: update table: %w", err)
	}
	return updated, nil
}

// UpdateStatus transitions a table to a new status, guarded on its expected
// current status (0 rows affected => ErrInvalidTransition). Pair with a
// preceding GetTableForUpdate in the same transaction, mirroring
// CheckRepo.UpdateStatus / OrderRepo.AdvanceStatus.
func (r *TableRepo) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status, expectedStatus domain.TableStatus) (domain.Table, error) {
	const q = `
		UPDATE tables SET status = $2, updated_at = NOW()
		WHERE id = $1 AND status = $3
		RETURNING id, tenant_id, branch_id, zone_id, name, capacity, status,
		          layout_position, is_active, created_at, updated_at`

	t, err := scanTable(tx.QueryRow(ctx, q, id, string(status), string(expectedStatus)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Table{}, ErrInvalidTransition
		}
		return domain.Table{}, fmt.Errorf("pos/repo/table: update status: %w", err)
	}
	return t, nil
}

// UpdateStatusIfCurrent transitions a table to newStatus only when it is
// currently in fromStatus, and reports whether the update was applied. Unlike
// UpdateStatus, a "no rows affected" outcome is not an error: it is used by
// CheckService.Close/Cancel to move a table to "cleaning" on a best-effort
// basis — if staff had already manually reset the table in the meantime, the
// check close/cancel must still succeed (see check_service.go).
func (r *TableRepo) UpdateStatusIfCurrent(ctx context.Context, tx pgx.Tx, id uuid.UUID, newStatus, fromStatus domain.TableStatus) (bool, error) {
	const q = `UPDATE tables SET status = $2, updated_at = NOW() WHERE id = $1 AND status = $3`
	tag, err := tx.Exec(ctx, q, id, string(newStatus), string(fromStatus))
	if err != nil {
		return false, fmt.Errorf("pos/repo/table: update status if current: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func scanTable(s interface{ Scan(...any) error }) (domain.Table, error) {
	var t domain.Table
	var status string
	if err := s.Scan(
		&t.ID, &t.TenantID, &t.BranchID, &t.ZoneID, &t.Name, &t.Capacity, &status,
		&t.LayoutPosition, &t.IsActive, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return domain.Table{}, err
	}
	t.Status = domain.TableStatus(status)
	return t, nil
}
