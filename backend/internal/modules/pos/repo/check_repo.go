package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// CheckRepo manages dine-in check (adisyon) persistence.
type CheckRepo struct{}

func NewCheckRepo() *CheckRepo { return &CheckRepo{} }

// checkColumns is the single projection every check query selects, in the
// exact order scanCheck reads them. It is shared rather than repeated per
// query because scanCheck takes ...any: a column list that drifts out of sync
// with the scan targets compiles fine and only fails at runtime.
const checkColumns = `id, tenant_id, branch_id, table_id, table_label, pax, status,
	          opened_by, opened_by_kind, source, closed_by, note, opened_at, closed_at,
	          created_at, updated_at`

// Create inserts a new open check and returns it with server-assigned fields.
//
// A unique_violation on checks_open_table_id_uidx is translated to
// ErrTableOccupied rather than left as a raw pg error: it can only happen
// when a table's status was manually reset to empty/reserved
// (TableService.SetStatus) while some other check still held it open — a
// state CheckService.Open's row lock cannot observe, since the lock is on
// the table row, not on "does any check already reference this table".
//
// OpenedByKind and Source are normalized to their 'staff'/'pos' defaults when
// the caller leaves them empty. The INSERT lists both columns explicitly, so
// an unset Go string would be written as an empty string and violate the CHECK
// rather than falling back to the column DEFAULT — the same reasoning that
// makes CheckService.Open own the Pax default (see domain.Check.Pax).
func (r *CheckRepo) Create(ctx context.Context, tx pgx.Tx, c domain.Check) (domain.Check, error) {
	const q = `
		INSERT INTO checks (tenant_id, branch_id, table_id, table_label, pax, status,
		                    opened_by, opened_by_kind, source, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING ` + checkColumns

	kind := c.OpenedByKind
	if kind == "" {
		kind = domain.OpenedByKindStaff
	}
	if (kind == domain.OpenedByKindStaff) != (c.OpenedBy != nil) {
		return domain.Check{}, ErrOpenedByKindMismatch
	}
	source := c.Source
	if source == "" {
		source = domain.SourcePOS
	}

	row := tx.QueryRow(ctx, q,
		c.TenantID, c.BranchID, c.TableID, c.TableLabel, c.Pax, string(c.Status),
		c.OpenedBy, string(kind), string(source), c.Note,
	)
	created, err := scanCheck(row)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Check{}, ErrTableOccupied
		}
		return domain.Check{}, err
	}
	return created, nil
}

// GetByID returns a check visible to the current tenant context.
func (r *CheckRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Check, error) {
	const q = `SELECT ` + checkColumns + ` FROM checks WHERE id = $1`

	c, err := scanCheck(tx.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Check{}, ErrNotFound
		}
		return domain.Check{}, fmt.Errorf("pos/repo/check: get by id: %w", err)
	}
	return c, nil
}

// GetForUpdate locks the check row for the duration of the caller's
// transaction. This is what actually prevents two concurrent Close/Cancel
// calls from both observing "open" and both emitting an outbox event: the
// second caller blocks here until the first commits or rolls back, then
// observes the already-updated status.
func (r *CheckRepo) GetForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Check, error) {
	const q = `SELECT ` + checkColumns + ` FROM checks WHERE id = $1 FOR UPDATE`

	c, err := scanCheck(tx.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Check{}, ErrNotFound
		}
		return domain.Check{}, fmt.Errorf("pos/repo/check: get for update: %w", err)
	}
	return c, nil
}

// GetOpenByTableForUpdate locks and returns the table's currently open check,
// or ErrNotFound when the table has none. At most one row can match:
// checks_open_table_id_uidx (pos/000004) enforces one open check per table.
//
// Guest order placement calls this BEFORE locking the table row, on purpose.
// CheckService.Close/Cancel take the locks in check → table order; a caller
// taking them table → check would deadlock against a cashier closing the same
// table's check while a diner submits a cart.
func (r *CheckRepo) GetOpenByTableForUpdate(ctx context.Context, tx pgx.Tx, tableID uuid.UUID) (domain.Check, error) {
	const q = `SELECT ` + checkColumns + ` FROM checks WHERE table_id = $1 AND status = 'open' FOR UPDATE`

	c, err := scanCheck(tx.QueryRow(ctx, q, tableID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Check{}, ErrNotFound
		}
		return domain.Check{}, fmt.Errorf("pos/repo/check: get open by table for update: %w", err)
	}
	return c, nil
}

// ListFilter narrows CheckRepo.List's result set. Both fields are optional
// (nil = no filter on that column) — mirroring service.ZonePatch/TablePatch's
// pointer-field convention for "not supplied" — so a caller can filter on
// status, branch_id, both, or neither without three near-duplicate queries.
// Tenant scoping is unaffected: RLS (SET LOCAL app.tenant_id via
// db.WithTenantReadTx) already restricts every row to the current tenant
// before either of these predicates is applied.
type ListFilter struct {
	Status   *domain.CheckStatus
	BranchID *uuid.UUID
}

// List returns checks visible to the current tenant (open first, then by
// opened_at desc), optionally narrowed by ListFilter. A nil field in filter
// is a NULL-guarded no-op predicate rather than a second/third query — see
// ListFilter's doc comment.
func (r *CheckRepo) List(ctx context.Context, tx pgx.Tx, filter ListFilter) ([]domain.Check, error) {
	const q = `
		SELECT ` + checkColumns + `
		FROM checks
		WHERE ($1::text IS NULL OR status = $1::text)
		  AND ($2::uuid IS NULL OR branch_id = $2::uuid)
		ORDER BY CASE status WHEN 'open' THEN 0 ELSE 1 END, opened_at DESC`

	var status *string
	if filter.Status != nil {
		s := string(*filter.Status)
		status = &s
	}

	rows, err := tx.Query(ctx, q, status, filter.BranchID)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/check: list: %w", err)
	}
	defer rows.Close()

	var out []domain.Check
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, fmt.Errorf("pos/repo/check: list scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetTotal returns the sum of all order items (quantity × unit_price_amount)
// for a check, counting only orders whose status is not one of
// domain.InactiveOrderStatuses (rejected/cancelled). A rejected or cancelled
// order's items must never be billed to the customer — see that variable's
// doc comment. Returns 0 if the check has no active orders.
//
// This delegates to TotalsByCheckIDs so there is exactly one query shape
// backing both the single-check and batch paths — see that method's doc
// comment for why that matters.
func (r *CheckRepo) GetTotal(ctx context.Context, tx pgx.Tx, checkID uuid.UUID) (int64, error) {
	totals, err := r.TotalsByCheckIDs(ctx, tx, []uuid.UUID{checkID})
	if err != nil {
		return 0, err
	}
	return totals[checkID], nil
}

// TotalsByCheckIDs batch-computes CheckRepo.GetTotal for many checks in a
// single query, so CheckService.List (and any other multi-check read) never
// needs one GetTotal round-trip per row (N+1). It applies the exact same
// exclusion as GetTotal — orders whose status is in
// domain.InactiveOrderStatuses (rejected/cancelled) never contribute — fed
// from that one shared slice, so the two can never silently drift apart.
//
// A check with no active orders (either none placed, or all
// rejected/cancelled) has no row in the GROUP BY result and is simply absent
// from the returned map; callers must treat a missing key as 0, not an
// error.
func (r *CheckRepo) TotalsByCheckIDs(ctx context.Context, tx pgx.Tx, checkIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	totals := make(map[uuid.UUID]int64, len(checkIDs))
	if len(checkIDs) == 0 {
		return totals, nil
	}

	excluded := make([]string, len(domain.InactiveOrderStatuses))
	for i, s := range domain.InactiveOrderStatuses {
		excluded[i] = string(s)
	}

	// checkIDs is sent as []string (cast to ::uuid[] in SQL), not
	// []uuid.UUID directly: this codebase runs every pool under
	// pgx.QueryExecModeSimpleProtocol (pgBouncer transaction-mode safety,
	// ADR-SEC-001/002 — see platform/db.go), which cannot resolve an array
	// element's OID without a server round-trip and fails array params of a
	// non-driver-native slice type with "cannot find encode plan". The
	// text[]-cast excluded-statuses parameter below already relies on this
	// same pattern.
	ids := make([]string, len(checkIDs))
	for i, id := range checkIDs {
		ids[i] = id.String()
	}

	rows, err := tx.Query(ctx, `
		SELECT o.check_id, SUM(oi.quantity * oi.unit_price_amount)
		FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		WHERE o.check_id = ANY($1::uuid[]) AND o.status <> ALL($2::text[])
		GROUP BY o.check_id
	`, ids, excluded)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/check: totals by check ids: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var checkID uuid.UUID
		var total int64
		if err := rows.Scan(&checkID, &total); err != nil {
			return nil, fmt.Errorf("pos/repo/check: totals by check ids scan: %w", err)
		}
		totals[checkID] = total
	}
	return totals, rows.Err()
}

// UpdateStatus transitions a check to a new status, guarded on its expected
// current status. Returns ErrInvalidTransition if the row's status no longer
// matches expectedStatus (0 rows affected). Callers should pair this with a
// preceding GetForUpdate in the same transaction: the row lock is what makes
// concurrent Close/Cancel calls serialize (only one observes "open"), while
// this guard is a defense-in-depth check against the expected status.
func (r *CheckRepo) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status, expectedStatus domain.CheckStatus, closedBy *uuid.UUID) (domain.Check, error) {
	const q = `
		UPDATE checks SET status = $2, closed_by = $4,
		                  closed_at = CASE WHEN $2 IN ('closed','cancelled') THEN NOW() ELSE closed_at END,
		                  updated_at = NOW()
		WHERE id = $1 AND status = $3
		RETURNING ` + checkColumns

	c, err := scanCheck(tx.QueryRow(ctx, q, id, string(status), string(expectedStatus), closedBy))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Check{}, ErrInvalidTransition
		}
		return domain.Check{}, fmt.Errorf("pos/repo/check: update status: %w", err)
	}
	return c, nil
}

// scanCheck reads one check row from any RowScanner (QueryRow or rows).
func scanCheck(s interface {
	Scan(...any) error
}) (domain.Check, error) {
	var c domain.Check
	var status, openedByKind, source string
	if err := s.Scan(
		&c.ID, &c.TenantID, &c.BranchID, &c.TableID, &c.TableLabel, &c.Pax, &status,
		&c.OpenedBy, &openedByKind, &source, &c.ClosedBy, &c.Note, &c.OpenedAt, &c.ClosedAt,
		&c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return domain.Check{}, err
	}
	c.Status = domain.CheckStatus(status)
	c.OpenedByKind = domain.OpenedByKind(openedByKind)
	c.Source = domain.Source(source)
	return c, nil
}
