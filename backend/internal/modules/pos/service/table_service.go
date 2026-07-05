package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// ErrManualOccupyForbidden is returned by TableService.SetStatus when the
// requested target status is "occupied". The state machine (domain/table.go)
// allows empty/reserved -> occupied because CheckService.Open needs that
// edge; but that transition must only ever happen as a side effect of
// opening a real check, never via the manual status endpoint — otherwise
// staff could mark a table occupied with no check backing it, and the
// eventual Close/Cancel -> cleaning handoff would have nothing to act on.
var ErrManualOccupyForbidden = errors.New("pos/service/table: table can only become occupied by opening a check")

// TablePlanEntry pairs a table with its zone's name/floor and the id of the
// check currently open against it (nil if none) — the shape the cash
// register needs to draw one row of the floor plan, grouped/labeled by zone
// without a second request.
type TablePlanEntry struct {
	Table         domain.Table
	ZoneName      string
	ZoneFloor     int
	ActiveCheckID *uuid.UUID
}

// TableService manages table zones and floor-plan tables.
type TableService struct {
	db        *db.Pool
	tableRepo *repo.TableRepo
	logger    *zap.Logger
}

// TableParams groups fx-injected dependencies.
type TableParams struct {
	fx.In

	DB        *db.Pool
	TableRepo *repo.TableRepo
	Logger    *zap.Logger
}

func NewTableService(p TableParams) *TableService {
	return &TableService{db: p.DB, tableRepo: p.TableRepo, logger: p.Logger}
}

// ---------------------------------------------------------------------------
// Zones
// ---------------------------------------------------------------------------

// CreateZone creates a new table zone for a branch. The acting principal
// must belong to the requested branch_id (ADR-AUTH-001 layer 3) — there is
// no persisted entity yet, so the client-supplied branch_id is validated
// directly.
func (s *TableService) CreateZone(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, z domain.TableZone) (domain.TableZone, error) {
	if err := requireBranch(ctx, principal, z.BranchID); err != nil {
		return domain.TableZone{}, err
	}
	z.TenantID = tenantID
	if !z.IsActive {
		z.IsActive = true
	}
	var created domain.TableZone
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.tableRepo.CreateZone(ctx, tx, z)
		return err
	})
	if err != nil {
		return domain.TableZone{}, fmt.Errorf("pos/service/table: create zone: %w", err)
	}
	return created, nil
}

// ListZones returns all zones for a branch. Read access is granted to every
// branch-facing role via pos.table.read (ADR-AUTH-001 layer 2); the branch
// scope itself is enforced here (layer 3).
func (s *TableService) ListZones(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, branchID uuid.UUID) ([]domain.TableZone, error) {
	if err := requireBranch(ctx, principal, branchID); err != nil {
		return nil, err
	}
	var zones []domain.TableZone
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		zones, err = s.tableRepo.ListZonesByBranch(ctx, tx, branchID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("pos/service/table: list zones: %w", err)
	}
	return zones, nil
}

// ZonePatch carries optional fields for a partial zone update (HTTP PATCH
// semantics): a nil field is left unchanged on the persisted row. This
// exists so PATCH /zones/{id} cannot silently zero out fields the client
// omitted (e.g. omitting is_active must NOT deactivate the zone) — the
// naive "decode into a full domain.TableZone and overwrite" approach that
// tempts a PATCH handler would do exactly that.
type ZonePatch struct {
	Name     *string
	Floor    *int
	IsActive *bool
}

// UpdateZone applies a partial update to a zone's name/floor/is_active. The
// acting principal must belong to the zone's branch — checked after
// loading, since the branch_id is only known once the row is loaded
// (mirrors CheckService.Close).
func (s *TableService) UpdateZone(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, zoneID uuid.UUID, patch ZonePatch) (domain.TableZone, error) {
	var updated domain.TableZone
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.tableRepo.GetZoneByID(ctx, tx, zoneID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, current.BranchID); err != nil {
			return err
		}
		if patch.Name != nil {
			current.Name = *patch.Name
		}
		if patch.Floor != nil {
			current.Floor = *patch.Floor
		}
		if patch.IsActive != nil {
			current.IsActive = *patch.IsActive
		}
		updated, err = s.tableRepo.UpdateZone(ctx, tx, current)
		return err
	})
	if err != nil {
		return domain.TableZone{}, wrapErr(err, "pos/service/table: update zone: %w")
	}
	return updated, nil
}

// ---------------------------------------------------------------------------
// Tables
// ---------------------------------------------------------------------------

// CreateTable creates a new table within a zone. The acting principal must
// belong to the requested branch_id; the zone must belong to the same
// tenant/branch (RLS already restricts tenant, the branch check is explicit
// here to catch a client attaching a table to another branch's zone).
func (s *TableService) CreateTable(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, t domain.Table) (domain.Table, error) {
	if err := requireBranch(ctx, principal, t.BranchID); err != nil {
		return domain.Table{}, err
	}
	t.TenantID = tenantID
	if !t.IsActive {
		t.IsActive = true
	}
	var created domain.Table
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		zone, err := s.tableRepo.GetZoneByID(ctx, tx, t.ZoneID)
		if err != nil {
			return err
		}
		if zone.BranchID != t.BranchID {
			return pub.ErrTableBranchMismatch
		}
		created, err = s.tableRepo.CreateTable(ctx, tx, t)
		return err
	})
	if err != nil {
		return domain.Table{}, wrapErr(err, "pos/service/table: create table: %w")
	}
	return created, nil
}

// ListTables returns every active table for a branch paired with its
// currently open check id, so the cash register can draw the entire floor
// plan (grouped by zone, client-side) from a single request.
func (s *TableService) ListTables(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, branchID uuid.UUID) ([]TablePlanEntry, error) {
	if err := requireBranch(ctx, principal, branchID); err != nil {
		return nil, err
	}
	var rows []repo.TableWithCheck
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = s.tableRepo.ListTablesByBranch(ctx, tx, branchID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("pos/service/table: list tables: %w", err)
	}
	out := make([]TablePlanEntry, len(rows))
	for i, r := range rows {
		out[i] = TablePlanEntry{
			Table:         r.Table,
			ZoneName:      r.ZoneName,
			ZoneFloor:     r.ZoneFloor,
			ActiveCheckID: r.ActiveCheckID,
		}
	}
	return out, nil
}

// TablePatch carries optional fields for a partial table update (HTTP PATCH
// semantics): a nil field is left unchanged on the persisted row — see
// ZonePatch's doc comment for why this matters (an omitted is_active/capacity
// must never be silently zeroed by a PATCH).
type TablePatch struct {
	ZoneID   *uuid.UUID
	Name     *string
	Capacity *int
	Layout   json.RawMessage // nil = unchanged; non-nil (including "{}") replaces
	IsActive *bool
}

// UpdateTable applies a partial update to a table's descriptive fields
// (name, capacity, zone_id, layout_position, is_active). Status never
// changes here — see SetStatus.
func (s *TableService) UpdateTable(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, tableID uuid.UUID, patch TablePatch) (domain.Table, error) {
	var updated domain.Table
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.tableRepo.GetTableByID(ctx, tx, tableID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, current.BranchID); err != nil {
			return err
		}
		if patch.ZoneID != nil && *patch.ZoneID != current.ZoneID {
			zone, err := s.tableRepo.GetZoneByID(ctx, tx, *patch.ZoneID)
			if err != nil {
				return err
			}
			if zone.BranchID != current.BranchID {
				return pub.ErrTableBranchMismatch
			}
			current.ZoneID = *patch.ZoneID
		}
		if patch.Name != nil {
			current.Name = *patch.Name
		}
		if patch.Capacity != nil {
			current.Capacity = *patch.Capacity
		}
		if patch.Layout != nil {
			current.LayoutPosition = patch.Layout
		}
		if patch.IsActive != nil {
			current.IsActive = *patch.IsActive
		}
		updated, err = s.tableRepo.UpdateTable(ctx, tx, current)
		return err
	})
	if err != nil {
		return domain.Table{}, wrapErr(err, "pos/service/table: update table: %w")
	}
	return updated, nil
}

// SetStatus manually transitions a table's status (reserved/cleaning/empty).
// "occupied" is rejected outright (ErrManualOccupyForbidden) — that edge is
// reserved for CheckService.Open, which drives it atomically alongside
// opening the check itself.
//
// The table row is locked (GetTableForUpdate) before the transition check,
// serializing this against a concurrent CheckService.Open on the same table
// exactly like CheckRepo.GetForUpdate serializes concurrent Close/Cancel.
func (s *TableService) SetStatus(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, tableID uuid.UUID, status domain.TableStatus) (domain.Table, error) {
	if status == domain.TableStatusOccupied {
		return domain.Table{}, ErrManualOccupyForbidden
	}
	if !status.Valid() {
		return domain.Table{}, fmt.Errorf("pos/service/table: invalid status %q", status)
	}
	var updated domain.Table
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.tableRepo.GetTableForUpdate(ctx, tx, tableID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, current.BranchID); err != nil {
			return err
		}
		if err := domain.TransitionTableStatus(current.Status, status); err != nil {
			return err
		}
		updated, err = s.tableRepo.UpdateStatus(ctx, tx, tableID, status, current.Status)
		return err
	})
	if err != nil {
		return domain.Table{}, wrapErr(err, "pos/service/table: set status: %w")
	}
	return updated, nil
}
