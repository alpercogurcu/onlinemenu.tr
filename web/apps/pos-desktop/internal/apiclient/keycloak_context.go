package apiclient

import (
	"context"
	"fmt"
	"net/http"
)

// This file holds the two backend calls the Keycloak login sequence makes
// before a CTX token exists (see
// backend/internal/modules/identity/http/me_handler.go's
// IsPreContext()-gated handlers): listing the memberships a
// Keycloak-authenticated person can select, and selecting one to obtain a
// CTX token. Both authenticate with an explicit Keycloak access token via
// doWithBearer (client.go) rather than this Client's own stored CTX token —
// see doWithBearer's doc comment for why that path also skips 401
// recovery. Orchestration (opening the browser, PKCE, deciding whether to
// auto-select a single membership) lives in main.App, not here — this
// Client stays a plain backend HTTP client.

// ContextItem mirrors contextItemDTO in
// backend/internal/modules/identity/http/me_handler.go — one selectable
// membership (tenant + optional branch + role) returned by
// GET /v1/identity/me/contexts.
type ContextItem struct {
	MembershipID string `json:"membership_id"`
	TenantID     string `json:"tenant_id"`
	TenantName   string `json:"tenant_name"`
	BranchID     string `json:"branch_id,omitempty"`
	BranchName   string `json:"branch_name,omitempty"`
	RoleID       string `json:"role_id"`
	RoleName     string `json:"role_name"`
}

type contextListResponse struct {
	Contexts []ContextItem `json:"contexts"`
	Customer bool          `json:"customer"`
}

// FetchKeycloakContexts calls GET /v1/identity/me/contexts with the given
// Keycloak access token, returning every membership (tenant/branch/role)
// the authenticated person can select. A staff-only POS station never
// expects the "customer" context shape (contextListResponse.Customer) —
// that field is intentionally not surfaced here.
func (c *Client) FetchKeycloakContexts(ctx context.Context, keycloakAccessToken string) ([]ContextItem, error) {
	var resp contextListResponse
	if err := c.doWithBearer(ctx, http.MethodGet, "/v1/identity/me/contexts", nil, &resp, keycloakAccessToken); err != nil {
		return nil, fmt.Errorf("apiclient: fetch keycloak contexts: %w", err)
	}
	return resp.Contexts, nil
}

type selectContextRequest struct {
	MembershipID string `json:"membership_id"`
}

type selectContextResponse struct {
	Token string `json:"token"`
}

// SelectKeycloakContext calls POST /v1/identity/auth/context with the given
// Keycloak access token and membership_id, returning the platform-signed
// CTX token. It does NOT install the returned token as this Client's
// current session token — callers (main.App) do that explicitly via
// SetSessionToken, since this method is also used by the CTX-401 recovery
// hook, which must control exactly when the new token becomes visible to
// the retried request.
func (c *Client) SelectKeycloakContext(ctx context.Context, keycloakAccessToken, membershipID string) (string, error) {
	var resp selectContextResponse
	body := selectContextRequest{MembershipID: membershipID}
	if err := c.doWithBearer(ctx, http.MethodPost, "/v1/identity/auth/context", body, &resp, keycloakAccessToken); err != nil {
		return "", fmt.Errorf("apiclient: select keycloak context: %w", err)
	}
	return resp.Token, nil
}
