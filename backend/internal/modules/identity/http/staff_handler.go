package http

import (
	"net/http"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/identity/service"
)

type inviteStaffRequest struct {
	FullName string     `json:"full_name"`
	Email    string     `json:"email"`
	BranchID *uuid.UUID `json:"branch_id,omitempty"`
	RoleID   uuid.UUID  `json:"role_id"`
}

type inviteStaffResponse struct {
	Person              personDetailResponse `json:"person"`
	Membership          membershipResponse   `json:"membership"`
	KeycloakUserCreated bool                 `json:"keycloak_user_created"`
	NotificationSent    bool                 `json:"notification_sent"`
	NotificationError   string               `json:"notification_error,omitempty"`
}

// InviteStaff handles POST /v1/identity/{tenantID}/staff (ADR-AUTH-003).
//
// Authorization: gated by identity.membership.create, the same action
// backing POST /{tenantID}/memberships (see routes.go). identity.* actions
// carry no seeded role_permissions rows at all — every one of them is
// reachable solely through the manager wildcard in
// configs/opa/bundles/authz.rego (`allow if has_role("manager")`). Reusing
// this action rather than minting a new one keeps that true — staff
// onboarding is manager-only in Faz 1, exactly like every other identity
// write — without adding a seed migration / rego rule / permission_wiring_test
// entry that would gate nothing a new (resource, action) pair doesn't already
// gate today.
func (h *Handler) InviteStaff(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}

	var body inviteStaffRequest
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.staffInvite.Invite(r.Context(), tenantID, service.StaffInviteRequest{
		FullName: body.FullName,
		Email:    body.Email,
		BranchID: body.BranchID,
		RoleID:   body.RoleID,
	})
	if err != nil {
		h.handleErr(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, toInviteStaffResponse(result))
}

func toInviteStaffResponse(r service.StaffInviteResult) inviteStaffResponse {
	return inviteStaffResponse{
		Person: personDetailResponse{
			ID:          r.Person.ID,
			KeycloakSub: r.Person.KeycloakSub,
			Email:       r.Person.Email,
			FullName:    r.Person.FullName,
			Phone:       r.Person.Phone,
		},
		Membership:          toMembershipResponse(r.Membership),
		KeycloakUserCreated: r.KeycloakUserCreated,
		NotificationSent:    r.NotificationSent,
		NotificationError:   r.NotificationError,
	}
}
