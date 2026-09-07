package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	pub "onlinemenu.tr/internal/modules/identity/public"
	tenantpub "onlinemenu.tr/internal/modules/tenant/public"
)

// validateBranch confirms branchID exists in tenantID via tenantReader (R2,
// docs/superpowers/plans/2026-09-07-deploy-oncesi-dialog.md). Shared by
// StaffInviteService.Invite and MembershipService.Create — identity carries
// no FK to tenant's branches table (module isolation), so this is the only
// check standing between a bogus branch_id and a membership row nothing else
// will ever catch.
//
// A nil branchID always skips the check: no branch scoping was requested.
//
// tenantReader is expected non-nil whenever branchID is non-nil — fx wires
// it as a required constructor param in production, so a nil reader here
// means a service was misconstructed (most likely a test built one by
// struct literal and forgot the field). That must fail closed with an
// explicit error, not be read as "no reader configured, skip validation":
// silently skipping would let an unvalidated branch_id straight into a
// membership write, which is exactly the class of bug R2 closes.
func validateBranch(ctx context.Context, tenantReader tenantpub.TenantReader, tenantID uuid.UUID, branchID *uuid.UUID) error {
	if branchID == nil {
		return nil
	}
	if tenantReader == nil {
		return errors.New("identity/service: tenant reader not configured, cannot validate branch_id")
	}
	if _, err := tenantReader.GetBranch(ctx, tenantID, *branchID); err != nil {
		if errors.Is(err, tenantpub.ErrNotFound) {
			return fmt.Errorf("%w: branch %s not found", pub.ErrInvalidInput, *branchID)
		}
		return fmt.Errorf("identity/service: get branch: %w", err)
	}
	return nil
}
