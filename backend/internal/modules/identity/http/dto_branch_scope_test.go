package http

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/identity/domain"
)

// TestToRoleResponse_CarriesBranchScoped is the regression for the 2026-09-20
// admin finding: toRoleResponse did not copy domain.Role.BranchScoped, so every
// role read back as chain-wide. The admin invite form therefore never showed a
// branch picker for Kasiyer/Garson/Mutfak, and the resulting POST /memberships
// (no branch_id) was refused by the memberships_branch_scope_guard trigger —
// ADR-SEC-005's last line of defence catching a UI that had been told the wrong
// thing.
//
// Scope() is NOT a substitute: it is derived from TenantID/BranchID and reports
// "system" for the seeded system roles, whereas BranchScoped is the flag that
// survives tenant cloning and is what RequiresBranch() actually consults.
func TestToRoleResponse_CarriesBranchScoped(t *testing.T) {
	tenantID := uuid.New()

	tests := []struct {
		name string
		role domain.Role
		want bool
	}{
		{
			name: "seeded branch-scoped system role",
			role: domain.Role{ID: uuid.New(), Name: "Kasiyer", SystemKey: "cashier", IsSystem: true, BranchScoped: true},
			want: true,
		},
		{
			name: "chain-wide system role",
			role: domain.Role{ID: uuid.New(), Name: "Yonetici", SystemKey: "manager", IsSystem: true},
			want: false,
		},
		{
			name: "custom tenant role marked branch-scoped",
			role: domain.Role{ID: uuid.New(), TenantID: &tenantID, Name: "Vardiya", BranchScoped: true},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := toRoleResponse(tt.role)
			assert.Equal(t, tt.want, resp.BranchScoped)

			// The admin form reads the JSON key, not the Go field.
			raw, err := json.Marshal(resp)
			require.NoError(t, err)
			var wire map[string]any
			require.NoError(t, json.Unmarshal(raw, &wire))
			assert.Equal(t, tt.want, wire["branch_scoped"])
		})
	}
}

// TestToMembershipDetailResponse_CarriesDisplayFields pins the other half of
// the same finding: POST /memberships answered with person_name, person_email
// and role_name empty, because it projected the bare domain.Membership while
// the list endpoint projected the joined detail. The admin user table renders
// those three fields, so a freshly invited person appeared as a blank row until
// the page was reloaded.
func TestToMembershipDetailResponse_CarriesDisplayFields(t *testing.T) {
	branchID := uuid.New()
	detail := domain.MembershipDetail{
		Membership: domain.Membership{
			ID:       uuid.New(),
			PersonID: uuid.New(),
			TenantID: uuid.New(),
			BranchID: &branchID,
			RoleID:   uuid.New(),
			Status:   domain.MembershipActive,
		},
		PersonName:  "Kasiyer B",
		PersonEmail: "kasiyer.b@dev.onlinemenu.tr",
		RoleName:    "Kasiyer",
	}

	resp := toMembershipDetailResponse(detail)

	assert.Equal(t, "Kasiyer B", resp.PersonName)
	assert.Equal(t, "kasiyer.b@dev.onlinemenu.tr", resp.PersonEmail)
	assert.Equal(t, "Kasiyer", resp.RoleName)
	assert.Equal(t, &branchID, resp.BranchID)
	assert.Equal(t, string(domain.MembershipActive), resp.Status)
}
