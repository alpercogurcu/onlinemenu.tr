package repo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	pub "onlinemenu.tr/internal/modules/tenant/public"
)

// HoursRepo provides data access for the branch_regular_hours and branch_special_hours tables.
type HoursRepo struct{}

// NewHoursRepo constructs a HoursRepo for fx injection.
func NewHoursRepo() *HoursRepo {
	return &HoursRepo{}
}

// GetRegularHours returns weekly schedule slots ordered by day_of_week and sort_order.
func (r *HoursRepo) GetRegularHours(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID) ([]pub.RegularHours, error) {
	const q = `
		SELECT id, tenant_id, branch_id, day_of_week, open_time, close_time,
		       crosses_midnight, is_closed, sort_order
		FROM branch_regular_hours
		WHERE tenant_id = $1 AND branch_id = $2
		ORDER BY day_of_week, sort_order`

	rows, err := tx.Query(ctx, q, tenantID, branchID)
	if err != nil {
		return nil, fmt.Errorf("tenant/repo: get regular hours: %w", err)
	}
	defer rows.Close()

	var result []pub.RegularHours
	for rows.Next() {
		h, err := scanRegularHours(rows)
		if err != nil {
			return nil, fmt.Errorf("tenant/repo: scan regular hours: %w", err)
		}
		result = append(result, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant/repo: get regular hours rows: %w", err)
	}
	if result == nil {
		result = []pub.RegularHours{}
	}
	return result, nil
}

// SetRegularHours atomically replaces the entire weekly schedule for a branch.
// DELETE and batch INSERT run in the same transaction; a failed INSERT triggers a full rollback.
func (r *HoursRepo) SetRegularHours(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID, hours []pub.RegularHours) error {
	const del = `DELETE FROM branch_regular_hours WHERE tenant_id = $1 AND branch_id = $2`
	if _, err := tx.Exec(ctx, del, tenantID, branchID); err != nil {
		return fmt.Errorf("tenant/repo: delete regular hours: %w", err)
	}
	if len(hours) == 0 {
		return nil
	}

	// Single round-trip batch INSERT instead of N individual Exec calls.
	const cols = 8
	args := make([]any, 0, len(hours)*cols)
	placeholders := make([]string, 0, len(hours))
	for i, h := range hours {
		base := i*cols + 1
		var openStr, closeStr *string
		if h.OpenTime != nil {
			s := h.OpenTime.String()
			openStr = &s
		}
		if h.CloseTime != nil {
			s := h.CloseTime.String()
			closeStr = &s
		}
		placeholders = append(placeholders, fmt.Sprintf(
			"($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base, base+1, base+2, base+3, base+4, base+5, base+6, base+7,
		))
		args = append(args,
			tenantID, branchID, isoWeekday(h.DayOfWeek), openStr, closeStr,
			h.CrossesMidnight, h.IsClosed, h.SortOrder,
		)
	}

	q := `INSERT INTO branch_regular_hours (
		tenant_id, branch_id, day_of_week, open_time, close_time,
		crosses_midnight, is_closed, sort_order
	) VALUES ` + strings.Join(placeholders, ",")

	if _, err := tx.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("tenant/repo: insert regular hours batch: %w", err)
	}
	return nil
}

// GetSpecialHours returns all special-day overrides ordered by date.
func (r *HoursRepo) GetSpecialHours(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID) ([]pub.SpecialHours, error) {
	const q = `
		SELECT id, tenant_id, branch_id, special_date, name, open_time, close_time,
		       crosses_midnight, is_closed
		FROM branch_special_hours
		WHERE tenant_id = $1 AND branch_id = $2
		ORDER BY special_date`

	rows, err := tx.Query(ctx, q, tenantID, branchID)
	if err != nil {
		return nil, fmt.Errorf("tenant/repo: get special hours: %w", err)
	}
	defer rows.Close()

	var result []pub.SpecialHours
	for rows.Next() {
		h, err := scanSpecialHours(rows)
		if err != nil {
			return nil, fmt.Errorf("tenant/repo: scan special hours: %w", err)
		}
		result = append(result, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant/repo: get special hours rows: %w", err)
	}
	if result == nil {
		result = []pub.SpecialHours{}
	}
	return result, nil
}

// UpsertSpecialHours inserts or replaces a single special-day override.
func (r *HoursRepo) UpsertSpecialHours(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID, sh pub.SpecialHours) error {
	var openStr, closeStr *string
	if sh.OpenTime != nil {
		s := sh.OpenTime.String()
		openStr = &s
	}
	if sh.CloseTime != nil {
		s := sh.CloseTime.String()
		closeStr = &s
	}

	const q = `
		INSERT INTO branch_special_hours (
			tenant_id, branch_id, special_date, name, open_time, close_time,
			crosses_midnight, is_closed
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (branch_id, special_date)
		DO UPDATE SET
			name = EXCLUDED.name,
			open_time = EXCLUDED.open_time,
			close_time = EXCLUDED.close_time,
			crosses_midnight = EXCLUDED.crosses_midnight,
			is_closed = EXCLUDED.is_closed,
			updated_at = NOW()`

	if _, err := tx.Exec(ctx, q,
		tenantID, branchID,
		// UTC date string avoids local-clock drift across timezones (ADR-DATA-003).
		sh.SpecialDate.UTC().Format(dateLayout),
		sh.Name, openStr, closeStr,
		sh.CrossesMidnight, sh.IsClosed,
	); err != nil {
		return fmt.Errorf("tenant/repo: upsert special hours: %w", err)
	}
	return nil
}

// DeleteSpecialHours removes the special-day override for the exact date.
func (r *HoursRepo) DeleteSpecialHours(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID, date time.Time) error {
	const q = `
		DELETE FROM branch_special_hours
		WHERE tenant_id = $1 AND branch_id = $2 AND special_date = $3`

	ct, err := tx.Exec(ctx, q, tenantID, branchID, date.UTC().Format(dateLayout))
	if err != nil {
		return fmt.Errorf("tenant/repo: delete special hours: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pub.ErrNotFound
	}
	return nil
}

// IsOpenAt reports whether a branch is open at the given instant.
// Special hours override regular hours when both match the same date.
func (r *HoursRepo) IsOpenAt(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID, at time.Time) (bool, error) {
	// Check for a special-hours record on the exact calendar date first.
	special, err := r.findSpecialHoursForDate(ctx, tx, tenantID, branchID, at)
	if err != nil && !errors.Is(err, pub.ErrNotFound) {
		return false, err
	}
	if err == nil {
		return isWithinWindow(at, special.OpenTime, special.CloseTime, special.CrossesMidnight, special.IsClosed), nil
	}

	// Fall back to regular hours for the weekday.
	slots, err := r.findRegularHoursForDay(ctx, tx, tenantID, branchID, at.Weekday())
	if err != nil {
		return false, err
	}
	for _, slot := range slots {
		if isWithinWindow(at, slot.OpenTime, slot.CloseTime, slot.CrossesMidnight, slot.IsClosed) {
			return true, nil
		}
	}
	return false, nil
}

func (r *HoursRepo) findSpecialHoursForDate(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID, at time.Time) (pub.SpecialHours, error) {
	const q = `
		SELECT id, tenant_id, branch_id, special_date, name, open_time, close_time,
		       crosses_midnight, is_closed
		FROM branch_special_hours
		WHERE tenant_id = $1 AND branch_id = $2 AND special_date = $3`

	// UTC date string avoids local-clock drift (ADR-DATA-003).
	row := tx.QueryRow(ctx, q, tenantID, branchID, at.UTC().Format(dateLayout))
	sh, err := scanSpecialHours(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pub.SpecialHours{}, pub.ErrNotFound
		}
		return pub.SpecialHours{}, fmt.Errorf("tenant/repo: find special hours for date: %w", err)
	}
	return sh, nil
}

func (r *HoursRepo) findRegularHoursForDay(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID, day time.Weekday) ([]pub.RegularHours, error) {
	const q = `
		SELECT id, tenant_id, branch_id, day_of_week, open_time, close_time,
		       crosses_midnight, is_closed, sort_order
		FROM branch_regular_hours
		WHERE tenant_id = $1 AND branch_id = $2 AND day_of_week = $3
		ORDER BY sort_order`

	rows, err := tx.Query(ctx, q, tenantID, branchID, isoWeekday(day))
	if err != nil {
		return nil, fmt.Errorf("tenant/repo: find regular hours for day: %w", err)
	}
	defer rows.Close()

	var slots []pub.RegularHours
	for rows.Next() {
		h, err := scanRegularHours(rows)
		if err != nil {
			return nil, fmt.Errorf("tenant/repo: scan regular hours: %w", err)
		}
		slots = append(slots, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant/repo: find regular hours for day rows: %w", err)
	}
	return slots, nil
}

// isWithinWindow reports whether `at` falls inside the open/close window.
// When crossesMidnight is true the close time is interpreted as being on the following day.
func isWithinWindow(at time.Time, open, close *pub.TimeOfDay, crossesMidnight, isClosed bool) bool {
	if isClosed || open == nil || close == nil {
		return false
	}

	atMins := at.Hour()*60 + at.Minute()
	openMins := open.Hour*60 + open.Minute
	closeMins := close.Hour*60 + close.Minute

	if !crossesMidnight {
		return atMins >= openMins && atMins < closeMins
	}
	// After-midnight window wraps: open..24:00 or 00:00..close.
	return atMins >= openMins || atMins < closeMins
}

// dateLayout is the ISO 8601 date format used for all DB date parameters.
const dateLayout = "2006-01-02"

// isoWeekday converts Go's time.Weekday (Sunday=0) to ISO 8601 (Monday=1, Sunday=7).
func isoWeekday(w time.Weekday) int {
	if w == time.Sunday {
		return 7
	}
	return int(w)
}

// goWeekday converts an ISO 8601 weekday (1=Mon … 7=Sun) to Go's time.Weekday.
func goWeekday(iso int) time.Weekday {
	if iso == 7 {
		return time.Sunday
	}
	return time.Weekday(iso)
}

// parseTimeOfDay parses a "HH:MM" or "HH:MM:SS" string from the database.
func parseTimeOfDay(s string) (*pub.TimeOfDay, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.SplitN(s, ":", 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid time format: %q", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, fmt.Errorf("parse hour %q: %w", parts[0], err)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("parse minute %q: %w", parts[1], err)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return nil, fmt.Errorf("time out of range: %q", s)
	}
	return &pub.TimeOfDay{Hour: h, Minute: m}, nil
}

func scanRegularHours(row rowScanner) (pub.RegularHours, error) {
	var (
		h            pub.RegularHours
		dayOfWeek    int
		openTimeStr  *string
		closeTimeStr *string
	)

	err := row.Scan(
		&h.ID, &h.TenantID, &h.BranchID, &dayOfWeek,
		&openTimeStr, &closeTimeStr,
		&h.CrossesMidnight, &h.IsClosed, &h.SortOrder,
	)
	if err != nil {
		return pub.RegularHours{}, err
	}

	h.DayOfWeek = goWeekday(dayOfWeek)

	if openTimeStr != nil {
		tod, err := parseTimeOfDay(*openTimeStr)
		if err != nil {
			return pub.RegularHours{}, fmt.Errorf("parse open_time: %w", err)
		}
		h.OpenTime = tod
	}
	if closeTimeStr != nil {
		tod, err := parseTimeOfDay(*closeTimeStr)
		if err != nil {
			return pub.RegularHours{}, fmt.Errorf("parse close_time: %w", err)
		}
		h.CloseTime = tod
	}

	return h, nil
}

func scanSpecialHours(row rowScanner) (pub.SpecialHours, error) {
	var (
		sh           pub.SpecialHours
		openTimeStr  *string
		closeTimeStr *string
	)

	err := row.Scan(
		&sh.ID, &sh.TenantID, &sh.BranchID, &sh.SpecialDate, &sh.Name,
		&openTimeStr, &closeTimeStr,
		&sh.CrossesMidnight, &sh.IsClosed,
	)
	if err != nil {
		return pub.SpecialHours{}, err
	}

	if openTimeStr != nil {
		tod, err := parseTimeOfDay(*openTimeStr)
		if err != nil {
			return pub.SpecialHours{}, fmt.Errorf("parse open_time: %w", err)
		}
		sh.OpenTime = tod
	}
	if closeTimeStr != nil {
		tod, err := parseTimeOfDay(*closeTimeStr)
		if err != nil {
			return pub.SpecialHours{}, fmt.Errorf("parse close_time: %w", err)
		}
		sh.CloseTime = tod
	}

	return sh, nil
}
