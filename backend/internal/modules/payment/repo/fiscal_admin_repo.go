package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/platform/db"
)

var (
	// ErrTerminalNotFound means no terminal with that id exists for the tenant.
	ErrTerminalNotFound = errors.New("payment/repo: fiscal terminal not found")
	// ErrTerminalSerialTaken means the serial is already registered — to another
	// tenant. (vendor, terminal_serial) is globally unique so an inbound webhook
	// resolves to exactly one tenant; a second claim on the same physical device
	// must be refused rather than silently rebound.
	ErrTerminalSerialTaken = errors.New("payment/repo: fiscal terminal serial already registered")
)

// FiscalTerminal is a registered fiscal device (ADR-FISCAL-002 §5).
type FiscalTerminal struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	BranchID          uuid.UUID
	Vendor            string
	TerminalSerial    string
	VendorMerchantRef string
	VendorBranchRef   string
	Label             string
	BasketMode        string
	IsActive          bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// FiscalDeviceSection is one device-synced tax section persisted for a terminal.
type FiscalDeviceSection struct {
	SectionNo    int
	Name         string
	TaxPermyriad int
	SyncedAt     time.Time
}

// FiscalSectionMapping ties a catalog category to a device section number.
type FiscalSectionMapping struct {
	CategoryID uuid.UUID
	SectionNo  int
}

// TerminalPatch carries the mutable fields of a terminal. A nil field is left
// untouched, which lets PATCH express "clear the label" (empty string) apart
// from "don't change the label" (absent).
type TerminalPatch struct {
	Label      *string
	BasketMode *string
	IsActive   *bool
}

// FiscalAdminRepo backs the fiscal admin API: terminal registry, device-synced
// sections and category→section mappings. Every method runs inside
// WithTenantTx/WithTenantReadTx so RLS scopes the rows (ADR-SEC-001).
type FiscalAdminRepo struct{ db *db.Pool }

func NewFiscalAdminRepo(pool *db.Pool) *FiscalAdminRepo { return &FiscalAdminRepo{db: pool} }

const terminalColumns = `id, tenant_id, branch_id, vendor, terminal_serial,
	COALESCE(vendor_merchant_ref, ''), COALESCE(vendor_branch_ref, ''),
	COALESCE(label, ''), basket_mode, is_active, created_at, updated_at`

func scanTerminal(row pgx.Row) (FiscalTerminal, error) {
	var t FiscalTerminal
	err := row.Scan(&t.ID, &t.TenantID, &t.BranchID, &t.Vendor, &t.TerminalSerial,
		&t.VendorMerchantRef, &t.VendorBranchRef, &t.Label, &t.BasketMode,
		&t.IsActive, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// UpsertTerminal registers a terminal, or re-pairs an already registered one.
//
// Re-registering the same (tenant, vendor, serial) — the natural outcome of an
// admin re-scanning the device QR — updates the existing row instead of
// failing, which makes POST idempotent without an Idempotency-Key. The
// conflict target is the tenant-scoped unique index; a collision on the
// GLOBAL (vendor, terminal_serial) index means another tenant owns the device
// and surfaces as ErrTerminalSerialTaken, never as a silent rebind.
func (r *FiscalAdminRepo) UpsertTerminal(ctx context.Context, t FiscalTerminal) (FiscalTerminal, error) {
	var out FiscalTerminal
	err := r.db.WithTenantTx(ctx, t.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO fiscal_terminals
				(tenant_id, branch_id, vendor, terminal_serial, vendor_merchant_ref,
				 vendor_branch_ref, label, basket_mode)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (tenant_id, vendor, terminal_serial) DO UPDATE SET
				branch_id           = EXCLUDED.branch_id,
				vendor_merchant_ref = EXCLUDED.vendor_merchant_ref,
				vendor_branch_ref   = EXCLUDED.vendor_branch_ref,
				label               = EXCLUDED.label,
				basket_mode         = EXCLUDED.basket_mode,
				is_active           = TRUE,
				updated_at          = now()
			RETURNING `+terminalColumns,
			t.TenantID, t.BranchID, t.Vendor, t.TerminalSerial,
			nullableText(t.VendorMerchantRef), nullableText(t.VendorBranchRef),
			nullableText(t.Label), t.BasketMode)

		var err error
		if out, err = scanTerminal(row); err != nil {
			if isUniqueViolation(err) {
				return ErrTerminalSerialTaken
			}
			return fmt.Errorf("payment/repo: upsert fiscal terminal: %w", err)
		}
		return nil
	})
	if err != nil {
		return FiscalTerminal{}, err
	}
	return out, nil
}

// ListTerminals returns a branch's terminals, oldest first — the same order
// FiscalTerminalDirectory.Resolve uses to pick the serving terminal, so the
// admin list reads top-down as "this is the one a sale will target".
func (r *FiscalAdminRepo) ListTerminals(ctx context.Context, tenantID, branchID uuid.UUID) ([]FiscalTerminal, error) {
	var out []FiscalTerminal
	err := r.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT `+terminalColumns+`
			FROM fiscal_terminals
			WHERE tenant_id = $1 AND branch_id = $2
			ORDER BY created_at
		`, tenantID, branchID)
		if err != nil {
			return fmt.Errorf("payment/repo: list fiscal terminals: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			t, err := scanTerminal(rows)
			if err != nil {
				return fmt.Errorf("payment/repo: scan fiscal terminal: %w", err)
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetTerminal loads one terminal by id within the tenant.
func (r *FiscalAdminRepo) GetTerminal(ctx context.Context, tenantID, id uuid.UUID) (FiscalTerminal, error) {
	var out FiscalTerminal
	err := r.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT `+terminalColumns+`
			FROM fiscal_terminals WHERE tenant_id = $1 AND id = $2
		`, tenantID, id)

		var err error
		if out, err = scanTerminal(row); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrTerminalNotFound
			}
			return fmt.Errorf("payment/repo: get fiscal terminal: %w", err)
		}
		return nil
	})
	if err != nil {
		return FiscalTerminal{}, err
	}
	return out, nil
}

// UpdateTerminal applies a partial update. Absent fields keep their value via
// COALESCE against the typed NULL parameter, so no read-modify-write race can
// clobber a concurrent change to a field this request never mentioned.
func (r *FiscalAdminRepo) UpdateTerminal(ctx context.Context, tenantID, id uuid.UUID, patch TerminalPatch) (FiscalTerminal, error) {
	var out FiscalTerminal
	err := r.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			UPDATE fiscal_terminals SET
				label       = COALESCE($3, label),
				basket_mode = COALESCE($4, basket_mode),
				is_active   = COALESCE($5, is_active),
				updated_at  = now()
			WHERE tenant_id = $1 AND id = $2
			RETURNING `+terminalColumns,
			tenantID, id, patch.Label, patch.BasketMode, patch.IsActive)

		var err error
		if out, err = scanTerminal(row); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrTerminalNotFound
			}
			return fmt.Errorf("payment/repo: update fiscal terminal: %w", err)
		}
		return nil
	})
	if err != nil {
		return FiscalTerminal{}, err
	}
	return out, nil
}

// ReplaceSections makes the stored sections of a terminal exactly match what
// the device reported: added, changed and removed sections all land in one
// transaction. Delete-then-insert (rather than upsert + delete-stale) keeps
// synced_at uniform and needs no array parameter — the pool runs
// QueryExecModeSimpleProtocol, where a typed int[] bind is a footgun.
//
// Rows are never referenced by fiscal_device_sections.id (mappings key on
// section_no), so recycling the surrogate ids is safe.
func (r *FiscalAdminRepo) ReplaceSections(ctx context.Context, tenantID, terminalID uuid.UUID, sections []domain.DeviceSection) error {
	return r.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			DELETE FROM fiscal_device_sections WHERE tenant_id = $1 AND terminal_id = $2
		`, tenantID, terminalID); err != nil {
			return fmt.Errorf("payment/repo: clear device sections: %w", err)
		}
		for _, s := range sections {
			if _, err := tx.Exec(ctx, `
				INSERT INTO fiscal_device_sections
					(tenant_id, terminal_id, section_no, name, tax_permyriad)
				VALUES ($1,$2,$3,$4,$5)
			`, tenantID, terminalID, s.SectionNo, s.Name, s.TaxPermyriad); err != nil {
				return fmt.Errorf("payment/repo: insert device section %d: %w", s.SectionNo, err)
			}
		}
		return nil
	})
}

// ListSections returns a terminal's device-synced sections, ordered by number.
func (r *FiscalAdminRepo) ListSections(ctx context.Context, tenantID, terminalID uuid.UUID) ([]FiscalDeviceSection, error) {
	var out []FiscalDeviceSection
	err := r.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT section_no, name, tax_permyriad, synced_at
			FROM fiscal_device_sections
			WHERE tenant_id = $1 AND terminal_id = $2
			ORDER BY section_no
		`, tenantID, terminalID)
		if err != nil {
			return fmt.Errorf("payment/repo: list device sections: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var s FiscalDeviceSection
			if err := rows.Scan(&s.SectionNo, &s.Name, &s.TaxPermyriad, &s.SyncedAt); err != nil {
				return fmt.Errorf("payment/repo: scan device section: %w", err)
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListSectionMappings returns a branch's category→section mappings.
func (r *FiscalAdminRepo) ListSectionMappings(ctx context.Context, tenantID, branchID uuid.UUID) ([]FiscalSectionMapping, error) {
	var out []FiscalSectionMapping
	err := r.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT category_id, section_no
			FROM fiscal_section_mappings
			WHERE tenant_id = $1 AND branch_id = $2
			ORDER BY section_no, category_id
		`, tenantID, branchID)
		if err != nil {
			return fmt.Errorf("payment/repo: list section mappings: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var m FiscalSectionMapping
			if err := rows.Scan(&m.CategoryID, &m.SectionNo); err != nil {
				return fmt.Errorf("payment/repo: scan section mapping: %w", err)
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReplaceSectionMappings swaps a branch's whole mapping set atomically. A
// partial write here would route some categories to a wrong device section and
// print a wrong VAT rate on a legal receipt, so the delete and the inserts
// share one transaction and an empty slice legitimately clears the branch.
//
// Requires DELETE on fiscal_section_mappings (payment/000005).
func (r *FiscalAdminRepo) ReplaceSectionMappings(ctx context.Context, tenantID, branchID uuid.UUID, mappings []FiscalSectionMapping) error {
	return r.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			DELETE FROM fiscal_section_mappings WHERE tenant_id = $1 AND branch_id = $2
		`, tenantID, branchID); err != nil {
			return fmt.Errorf("payment/repo: clear section mappings: %w", err)
		}
		for _, m := range mappings {
			if _, err := tx.Exec(ctx, `
				INSERT INTO fiscal_section_mappings (tenant_id, branch_id, category_id, section_no)
				VALUES ($1,$2,$3,$4)
			`, tenantID, branchID, m.CategoryID, m.SectionNo); err != nil {
				return fmt.Errorf("payment/repo: insert section mapping for category %s: %w", m.CategoryID, err)
			}
		}
		return nil
	})
}
