package tokenx

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// sectionsFunc adapts a function to SectionResolver.
type sectionsFunc func(ctx context.Context, tenantID, branchID, categoryID uuid.UUID) (int, int, error)

func (f sectionsFunc) Resolve(ctx context.Context, tenantID, branchID, categoryID uuid.UUID) (int, int, error) {
	return f(ctx, tenantID, branchID, categoryID)
}

// staticSections resolves every category to the same section and tax rate.
func staticSections(sectionNo, taxPermyriad int) SectionResolver {
	return sectionsFunc(func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (int, int, error) {
		return sectionNo, taxPermyriad, nil
	})
}

// terminalsFunc adapts a function to TerminalResolver.
type terminalsFunc func(ctx context.Context, tenantID, branchID uuid.UUID) (TerminalRef, error)

func (f terminalsFunc) Resolve(ctx context.Context, tenantID, branchID uuid.UUID) (TerminalRef, error) {
	return f(ctx, tenantID, branchID)
}

func staticTerminal(ref TerminalRef) TerminalResolver {
	return terminalsFunc(func(context.Context, uuid.UUID, uuid.UUID) (TerminalRef, error) {
		return ref, nil
	})
}
