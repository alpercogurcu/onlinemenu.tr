package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/zalando/go-keyring"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
	"onlinemenu.tr/pos-desktop/internal/keycloakauth"
	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// This file exercises App.LoginWithKeycloak/SelectKeycloakContext end-to-end
// against httptest fakes (a fake Keycloak IdP + a fake backend), using the
// injectable openURL seam (see app.go's doc comment on the field) instead of
// a real Wails runtime/browser — runtime.BrowserOpenURL and friends panic
// (log.Fatalf) outside a real Wails lifecycle context, so tests never call
// NewApp/startup; they build an App literal directly, matching how apiclient
// and keycloakauth are already tested in isolation.

// newTestApp wires an App whose api/kc/kcStore point at the given httptest
// servers (nil-safe: pass "" to leave a field unconfigured for a test that
// doesn't need it).
func newTestApp(t *testing.T, kcBaseURL, backendBaseURL string) *App {
	t.Helper()
	keyring.MockInit()
	dir := t.TempDir()

	return &App{
		ctx:     context.Background(),
		api:     apiclient.New(backendBaseURL, tokenstore.New(dir, nil)),
		kc:      keycloakauth.New(keycloakauth.Config{BaseURL: kcBaseURL, Realm: "onlinemenu", ClientID: keycloakClientID}),
		kcStore: tokenstore.NewKeycloak(dir, nil),
		openURL: func(string) {}, // overridden per-test below
	}
}

// fireLoopbackCallback extracts redirect_uri (and optionally state/nonce)
// from an authorize URL built by keycloakauth.Client.AuthorizeURL and fires
// a GET against it — simulating the system browser completing the Keycloak
// login form and being redirected back to the loopback listener.
func fireLoopbackCallback(t *testing.T, authURL, codeOverride, stateOverride string) {
	t.Helper()
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authURL: %v", err)
	}
	q := parsed.Query()
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	if stateOverride != "" {
		state = stateOverride
	}
	code := "auth-code-1"
	if codeOverride != "" {
		code = codeOverride
	}

	cbURL := redirectURI + "?" + url.Values{"code": {code}, "state": {state}}.Encode()
	go func() {
		resp, err := http.Get(cbURL)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
}

func fakeIDToken(t *testing.T, nonce string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, err := json.Marshal(map[string]string{"nonce": nonce})
	if err != nil {
		t.Fatalf("marshal id_token payload: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// fakeKeycloakTokenServer returns a Keycloak token-endpoint double: it
// serves both the authorization_code and refresh_token grants identically
// (tokenJSON is a func so callers can vary the response per call, e.g. to
// rotate the refresh token).
func fakeKeycloakTokenServer(t *testing.T, tokenJSON func(r *http.Request) string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tokenJSON(r)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type fakeContextItem struct {
	MembershipID string
	TenantID     string
	TenantName   string
	BranchID     string
	BranchName   string
	RoleID       string
	RoleName     string
}

// fakeBackend serves GET /v1/identity/me/contexts, POST
// /v1/identity/auth/context and GET /v1/identity/me — the three backend
// calls the Keycloak login sequence makes.
func fakeBackend(t *testing.T, contexts []fakeContextItem, ctxToken string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/identity/me/contexts", func(w http.ResponseWriter, r *http.Request) {
		type item struct {
			MembershipID string `json:"membership_id"`
			TenantID     string `json:"tenant_id"`
			TenantName   string `json:"tenant_name"`
			BranchID     string `json:"branch_id,omitempty"`
			BranchName   string `json:"branch_name,omitempty"`
			RoleID       string `json:"role_id"`
			RoleName     string `json:"role_name"`
		}
		items := make([]item, len(contexts))
		for i, c := range contexts {
			items[i] = item{c.MembershipID, c.TenantID, c.TenantName, c.BranchID, c.BranchName, c.RoleID, c.RoleName}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"contexts": items, "customer": false})
	})
	mux.HandleFunc("/v1/identity/auth/context", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": ctxToken})
	})
	mux.HandleFunc("/v1/identity/me", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"person": map[string]string{"id": "person-1", "email": "cashier@example.com", "full_name": "Cashier One"},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestLoginWithKeycloak_StateMismatch_Rejected(t *testing.T) {
	// The token endpoint must never be reached once state validation fails.
	kcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("token endpoint must not be called after a state mismatch")
	}))
	defer kcSrv.Close()

	a := newTestApp(t, kcSrv.URL, "http://unused.invalid")
	a.openURL = func(authURL string) {
		fireLoopbackCallback(t, authURL, "", "deliberately-wrong-state")
	}

	_, err := a.LoginWithKeycloak()
	if err == nil {
		t.Fatal("LoginWithKeycloak: want error on state mismatch, got nil")
	}
}

func TestLoginWithKeycloak_NonceMismatch_Rejected(t *testing.T) {
	kcSrv := fakeKeycloakTokenServer(t, func(r *http.Request) string {
		idToken := fakeIDToken(t, "a-nonce-that-does-not-match-the-generated-one")
		return `{"access_token":"at-1","refresh_token":"rt-1","id_token":"` + idToken + `","expires_in":300}`
	})

	a := newTestApp(t, kcSrv.URL, "http://unused.invalid")
	a.openURL = func(authURL string) {
		fireLoopbackCallback(t, authURL, "", "")
	}

	_, err := a.LoginWithKeycloak()
	if err == nil {
		t.Fatal("LoginWithKeycloak: want error on id_token nonce mismatch, got nil")
	}
}

func TestLoginWithKeycloak_SingleMembership_AutoSelectsAndPersistsRefresh(t *testing.T) {
	kcSrv := fakeKeycloakTokenServer(t, func(r *http.Request) string {
		return `{"access_token":"kc-access-1","refresh_token":"kc-refresh-1","expires_in":300}`
	})
	backendSrv := fakeBackend(t, []fakeContextItem{
		{MembershipID: "m1", TenantID: "t1", TenantName: "Tenant One", RoleID: "r1", RoleName: "cashier"},
	}, "ctx-token-1")

	a := newTestApp(t, kcSrv.URL, backendSrv.URL)
	a.openURL = func(authURL string) {
		fireLoopbackCallback(t, authURL, "", "")
	}

	result, err := a.LoginWithKeycloak()
	if err != nil {
		t.Fatalf("LoginWithKeycloak: %v", err)
	}
	if result.NeedsContextSelection {
		t.Fatal("NeedsContextSelection = true, want false (single membership auto-selects)")
	}
	if !result.Session.Authenticated || result.Session.UserID != "person-1" {
		t.Fatalf("unexpected session: %+v", result.Session)
	}
	if !a.api.IsAuthenticated() {
		t.Fatal("apiclient should hold the CTX token after auto-select")
	}

	state, err := keycloakauth.LoadSessionState(a.kcStore)
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if state.RefreshToken != "kc-refresh-1" || state.MembershipID != "m1" {
		t.Fatalf("persisted state = %+v, want refresh=kc-refresh-1 membership=m1", state)
	}
}

func TestLoginWithKeycloak_MultiMembership_ReturnsContextsForPicker(t *testing.T) {
	kcSrv := fakeKeycloakTokenServer(t, func(r *http.Request) string {
		return `{"access_token":"kc-access-1","refresh_token":"kc-refresh-1","expires_in":300}`
	})
	backendSrv := fakeBackend(t, []fakeContextItem{
		{MembershipID: "m1", TenantID: "t1", TenantName: "Tenant One", RoleID: "r1", RoleName: "cashier"},
		{MembershipID: "m2", TenantID: "t1", TenantName: "Tenant One", BranchID: "b1", BranchName: "Şube 1", RoleID: "r1", RoleName: "cashier"},
	}, "ctx-token-1")

	a := newTestApp(t, kcSrv.URL, backendSrv.URL)
	a.openURL = func(authURL string) {
		fireLoopbackCallback(t, authURL, "", "")
	}

	result, err := a.LoginWithKeycloak()
	if err != nil {
		t.Fatalf("LoginWithKeycloak: %v", err)
	}
	if !result.NeedsContextSelection || len(result.Contexts) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Session.Authenticated {
		t.Fatal("Session.Authenticated = true, want false before context is picked")
	}
	if a.api.IsAuthenticated() {
		t.Fatal("apiclient must not hold a CTX token before context is picked")
	}

	// Refresh token is already durable (so a restart before picking still
	// has something to restore-attempt from); membership is intentionally
	// still unset.
	state, err := keycloakauth.LoadSessionState(a.kcStore)
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if state.RefreshToken != "kc-refresh-1" || state.MembershipID != "" {
		t.Fatalf("persisted state = %+v, want refresh=kc-refresh-1 membership=\"\"", state)
	}

	// The frontend now calls SelectKeycloakContext with the cashier's pick.
	session, err := a.SelectKeycloakContext("m2")
	if err != nil {
		t.Fatalf("SelectKeycloakContext: %v", err)
	}
	if !session.Authenticated {
		t.Fatal("SelectKeycloakContext: session not authenticated")
	}
	state, err = keycloakauth.LoadSessionState(a.kcStore)
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if state.MembershipID != "m2" || state.RefreshToken != "kc-refresh-1" {
		t.Fatalf("persisted state after pick = %+v, want refresh=kc-refresh-1 membership=m2", state)
	}
}

func TestLoginWithKeycloak_NoMembership_ReturnsError(t *testing.T) {
	kcSrv := fakeKeycloakTokenServer(t, func(r *http.Request) string {
		return `{"access_token":"kc-access-1","refresh_token":"kc-refresh-1","expires_in":300}`
	})
	backendSrv := fakeBackend(t, nil, "unused")

	a := newTestApp(t, kcSrv.URL, backendSrv.URL)
	a.openURL = func(authURL string) {
		fireLoopbackCallback(t, authURL, "", "")
	}

	_, err := a.LoginWithKeycloak()
	if err == nil {
		t.Fatal("LoginWithKeycloak: want error when no membership is found, got nil")
	}
}

// TestPersistKeycloakRefresh_PreservesExistingMembership guards a
// read-modify-write bug that would silently discard the picked membership
// on every refresh-token rotation, forcing the cashier back through the
// context picker on every restart.
func TestPersistKeycloakRefresh_PreservesExistingMembership(t *testing.T) {
	keyring.MockInit()
	a := &App{kcStore: tokenstore.NewKeycloak(t.TempDir(), nil)}

	if err := a.persistKeycloakRefresh("rt-1", "membership-1"); err != nil {
		t.Fatalf("persistKeycloakRefresh: %v", err)
	}
	// Simulate a later token rotation where no membership is explicitly
	// passed (see currentKeycloakAccessToken's call site).
	if err := a.persistKeycloakRefresh("rt-2", ""); err != nil {
		t.Fatalf("persistKeycloakRefresh: %v", err)
	}

	state, err := keycloakauth.LoadSessionState(a.kcStore)
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if state.RefreshToken != "rt-2" || state.MembershipID != "membership-1" {
		t.Fatalf("state = %+v, want refresh rotated to rt-2, membership-1 preserved", state)
	}
}

// TestPersistKeycloakMembership_PreservesExistingRefreshToken is
// persistKeycloakRefresh's mirror image: selecting/reselecting a
// membership must not discard the already-persisted refresh token.
func TestPersistKeycloakMembership_PreservesExistingRefreshToken(t *testing.T) {
	keyring.MockInit()
	a := &App{kcStore: tokenstore.NewKeycloak(t.TempDir(), nil)}

	if err := a.persistKeycloakRefresh("rt-1", ""); err != nil {
		t.Fatalf("persistKeycloakRefresh: %v", err)
	}
	if err := a.persistKeycloakMembership("membership-2"); err != nil {
		t.Fatalf("persistKeycloakMembership: %v", err)
	}

	state, err := keycloakauth.LoadSessionState(a.kcStore)
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if state.RefreshToken != "rt-1" || state.MembershipID != "membership-2" {
		t.Fatalf("state = %+v, want refresh-token rt-1 preserved, membership-2 set", state)
	}
}

// TestTryRestoreSession_KeycloakRefreshRestoresSessionAndRotatesToken is the
// silent-restore path's integration test (task flow step 4: "açılışta
// keychain'de refresh varsa sessiz yenileme dene → başarılıysa login ekranı
// atlanır"). It seeds a persisted refresh_token + membership_id (as a prior
// LoginWithKeycloak call would have left behind), then verifies
// TryRestoreSession silently derives a fresh CTX session from it AND
// persists the rotated refresh token the fake IdP returns — proving
// rotation survives, not just that Refresh was called.
func TestTryRestoreSession_KeycloakRefreshRestoresSessionAndRotatesToken(t *testing.T) {
	kcSrv := fakeKeycloakTokenServer(t, func(r *http.Request) string {
		return `{"access_token":"kc-access-2","refresh_token":"kc-refresh-rotated","expires_in":300}`
	})
	backendSrv := fakeBackend(t, []fakeContextItem{
		{MembershipID: "m1", TenantID: "t1", TenantName: "Tenant One", RoleID: "r1", RoleName: "cashier"},
	}, "ctx-token-restored")

	a := newTestApp(t, kcSrv.URL, backendSrv.URL)
	if err := keycloakauth.SaveSessionState(a.kcStore, keycloakauth.SessionState{
		RefreshToken: "kc-refresh-original",
		MembershipID: "m1",
	}); err != nil {
		t.Fatalf("seed SaveSessionState: %v", err)
	}

	session, err := a.TryRestoreSession()
	if err != nil {
		t.Fatalf("TryRestoreSession: %v", err)
	}
	if !session.Authenticated || session.UserID != "person-1" {
		t.Fatalf("unexpected session: %+v", session)
	}
	if !a.api.IsAuthenticated() {
		t.Fatal("apiclient should hold a CTX token after a restored session")
	}

	state, err := keycloakauth.LoadSessionState(a.kcStore)
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if state.RefreshToken != "kc-refresh-rotated" {
		t.Fatalf("RefreshToken = %q, want rotated value kc-refresh-rotated persisted", state.RefreshToken)
	}
	if state.MembershipID != "m1" {
		t.Fatalf("MembershipID = %q, want m1 preserved across restore", state.MembershipID)
	}
}

// TestTryRestoreSession_NoPersistedSession_FallsBackToDevLoginWhoAmI guards
// the "neither flow has a session" case: no Keycloak state, no dev-login CTX
// token — TryRestoreSession must return Authenticated:false, nil (not an
// error), matching WhoAmI's own "not logged in is not a failure" contract.
func TestTryRestoreSession_NoPersistedSession_FallsBackToDevLoginWhoAmI(t *testing.T) {
	a := newTestApp(t, "http://unused.invalid", "http://unused.invalid")

	session, err := a.TryRestoreSession()
	if err != nil {
		t.Fatalf("TryRestoreSession: %v", err)
	}
	if session.Authenticated {
		t.Fatalf("session = %+v, want Authenticated:false with no persisted session anywhere", session)
	}
}

// TestRecoverKeycloakContext_NoMembershipOnRecord guards the recovery hook
// against a nil/empty membership (e.g. wired before any context was ever
// selected) — it must fail fast with a descriptive error rather than call
// the backend with an empty membership_id.
func TestRecoverKeycloakContext_NoMembershipOnRecord(t *testing.T) {
	a := newTestApp(t, "http://unused.invalid", "http://unused.invalid")

	_, err := a.recoverKeycloakContext(context.Background())
	if err == nil {
		t.Fatal("recoverKeycloakContext: want error when no membership is on record, got nil")
	}
}
