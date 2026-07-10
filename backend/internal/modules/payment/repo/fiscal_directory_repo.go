package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/payment/fiscal/tokenx"
	"onlinemenu.tr/internal/platform/db"
)

var (
	// ErrNoActiveTerminal means the branch has no registered, active fiscal
	// terminal for the vendor — the admin must pair a device first.
	ErrNoActiveTerminal = errors.New("payment/repo: no active fiscal terminal for branch")
	// ErrNoSectionMapping means the catalog category was never mapped to a
	// device section. Refusing here keeps a wrong tax rate off a legal receipt.
	ErrNoSectionMapping = errors.New("payment/repo: no fiscal section mapping for category")
)

// FiscalTerminalDirectory resolves which Token terminal serves a branch.
// It implements tokenx.TerminalResolver.
type FiscalTerminalDirectory struct{ db *db.Pool }

func NewFiscalTerminalDirectory(pool *db.Pool) *FiscalTerminalDirectory {
	return &FiscalTerminalDirectory{db: pool}
}

var _ tokenx.TerminalResolver = (*FiscalTerminalDirectory)(nil)

func (d *FiscalTerminalDirectory) Resolve(ctx context.Context, tenantID, branchID uuid.UUID) (tokenx.TerminalRef, error) {
	var ref tokenx.TerminalRef
	err := d.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		// Oldest active registration wins so the pick is deterministic when a
		// branch runs several devices; the basket still reaches every terminal
		// in list mode (vendor broadcasts by branch).
		row := tx.QueryRow(ctx, `
			SELECT terminal_serial, COALESCE(vendor_branch_ref, ''), basket_mode
			FROM fiscal_terminals
			WHERE tenant_id = $1 AND branch_id = $2 AND vendor = 'tokenx' AND is_active
			ORDER BY created_at
			LIMIT 1
		`, tenantID, branchID)

		var mode string
		if err := row.Scan(&ref.Serial, &ref.VendorBranchRef, &mode); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNoActiveTerminal
			}
			return fmt.Errorf("payment/repo: resolve terminal: %w", err)
		}
		ref.Mode = tokenx.BasketMode(mode)
		return nil
	})
	if err != nil {
		return tokenx.TerminalRef{}, err
	}
	return ref, nil
}

// FiscalSectionDirectory resolves a catalog category to the device section and
// its tax rate. It implements tokenx.SectionResolver.
type FiscalSectionDirectory struct{ db *db.Pool }

func NewFiscalSectionDirectory(pool *db.Pool) *FiscalSectionDirectory {
	return &FiscalSectionDirectory{db: pool}
}

var _ tokenx.SectionResolver = (*FiscalSectionDirectory)(nil)

func (d *FiscalSectionDirectory) Resolve(ctx context.Context, tenantID, branchID, categoryID uuid.UUID) (int, int, error) {
	var sectionNo, taxPermyriad int
	err := d.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		// The mapping names a section number; the tax rate comes from the
		// device-synced section of the branch's active terminal, never from
		// the mapping itself, so a device-side tax change is picked up on the
		// next sync instead of silently diverging.
		row := tx.QueryRow(ctx, `
			SELECT m.section_no, s.tax_permyriad
			FROM fiscal_section_mappings m
			JOIN fiscal_terminals t
			  ON t.tenant_id = m.tenant_id AND t.branch_id = m.branch_id
			 AND t.vendor = 'tokenx' AND t.is_active
			JOIN fiscal_device_sections s
			  ON s.tenant_id = m.tenant_id AND s.terminal_id = t.id
			 AND s.section_no = m.section_no
			WHERE m.tenant_id = $1 AND m.branch_id = $2 AND m.category_id = $3
			ORDER BY t.created_at
			LIMIT 1
		`, tenantID, branchID, categoryID)

		if err := row.Scan(&sectionNo, &taxPermyriad); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNoSectionMapping
			}
			return fmt.Errorf("payment/repo: resolve section: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return sectionNo, taxPermyriad, nil
}
