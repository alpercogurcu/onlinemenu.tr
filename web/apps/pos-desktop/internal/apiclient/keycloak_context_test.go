package apiclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchKeycloakContexts_SendsBearerTokenAndParsesList(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/identity/me/contexts" || r.Method != http.MethodGet {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotAuthHeader = r.Header.Get("Authorization")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(contextListResponse{
			Contexts: []ContextItem{
				{MembershipID: "m1", TenantID: "t1", TenantName: "Tenant One", RoleID: "r1", RoleName: "cashier"},
				{MembershipID: "m2", TenantID: "t1", TenantName: "Tenant One", BranchID: "b1", BranchName: "Şube 1", RoleID: "r1", RoleName: "cashier"},
			},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{})

	items, err := c.FetchKeycloakContexts(t.Context(), "keycloak-access-token")
	if err != nil {
		t.Fatalf("FetchKeycloakContexts: %v", err)
	}
	if gotAuthHeader != "Bearer keycloak-access-token" {
		t.Fatalf("Authorization = %q, want Bearer keycloak-access-token", gotAuthHeader)
	}
	if len(items) != 2 || items[1].BranchID != "b1" {
		t.Fatalf("unexpected items: %+v", items)
	}

	// This is a pre-context call: it must never touch the CTX token.
	if c.IsAuthenticated() {
		t.Fatal("FetchKeycloakContexts must not establish a CTX session")
	}
}

func TestSelectKeycloakContext_SendsBearerTokenAndMembershipID_DoesNotSetSessionToken(t *testing.T) {
	var gotAuthHeader string
	var gotBody selectContextRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/identity/auth/context" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotAuthHeader = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(selectContextResponse{Token: "ctx-token-1"})
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{})

	tok, err := c.SelectKeycloakContext(t.Context(), "keycloak-access-token", "membership-1")
	if err != nil {
		t.Fatalf("SelectKeycloakContext: %v", err)
	}
	if tok != "ctx-token-1" {
		t.Fatalf("token = %q, want ctx-token-1", tok)
	}
	if gotAuthHeader != "Bearer keycloak-access-token" {
		t.Fatalf("Authorization = %q, want Bearer keycloak-access-token", gotAuthHeader)
	}
	if gotBody.MembershipID != "membership-1" {
		t.Fatalf("membership_id = %q, want membership-1", gotBody.MembershipID)
	}

	// SelectKeycloakContext returns the token but must not install it —
	// callers (main.App / the CTX-401 recovery hook) decide when.
	if c.IsAuthenticated() {
		t.Fatal("SelectKeycloakContext must not install the CTX token itself")
	}
}

func TestSetSessionToken_InstallsTokenWithoutPersistingToStore(t *testing.T) {
	store := &memStore{}
	c := New("http://unused.invalid", store)

	c.SetSessionToken("ctx-token-in-memory")

	if !c.IsAuthenticated() {
		t.Fatal("client should be authenticated after SetSessionToken")
	}
	if store.saved {
		t.Fatal("SetSessionToken must not persist to the token store (Keycloak CTX tokens stay in-memory only)")
	}
}
