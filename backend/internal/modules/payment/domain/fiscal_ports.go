package domain

import (
	"context"

	"github.com/google/uuid"
)

// BasketMode selects the endpoint used to deliver a basket to a fiscal device.
// The mode is a physical setting on the device; the backend must match it per
// terminal.
type BasketMode string

const (
	// BasketModeInstant delivers straight to one terminal, which jumps to the
	// payment screen. This is the default flow.
	BasketModeInstant BasketMode = "instant"
	// BasketModeList lists the basket on every terminal of the branch, to be
	// picked by the cashier.
	BasketModeList BasketMode = "list"
)

// Valid reports whether the mode is a recognised value.
func (m BasketMode) Valid() bool {
	return m == BasketModeInstant || m == BasketModeList
}

// SectionResolver maps a catalog category to a device section (kısım).
// Implementations read the tenant's synchronized fiscal_section_mappings; an
// adapter never guesses a sectionNo, because a wrong section means a wrong tax
// rate on a legal receipt (ADR-FISCAL-002 §2).
type SectionResolver interface {
	Resolve(ctx context.Context, tenantID, branchID, categoryID uuid.UUID) (sectionNo int, taxPermyriad int, err error)
}

// TerminalRef identifies where a basket must be delivered. VendorBranchRef is
// the vendor's branch id used as the branch-id header in list mode; it varies
// per basket and therefore comes from the resolver, never from static config.
type TerminalRef struct {
	Serial          string
	VendorBranchRef string
	Mode            BasketMode
}

// TerminalResolver picks the terminal that serves a branch.
//
// SectionResolver and TerminalResolver are declared here, not in the vendor
// adapter package, because they are ports: the repo layer implements them and
// the adapter consumes them. Declaring them in the adapter would force the
// repo to import a vendor package (payment_repo -> payment_fiscal), inverting
// the intended dependency direction.
type TerminalResolver interface {
	Resolve(ctx context.Context, tenantID, branchID uuid.UUID) (TerminalRef, error)
}
