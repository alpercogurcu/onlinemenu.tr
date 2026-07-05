package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
	"onlinemenu.tr/pos-desktop/internal/config"
	"onlinemenu.tr/pos-desktop/internal/hardware"
	"onlinemenu.tr/pos-desktop/internal/keycloakauth"
	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// appDataDirName names the per-OS user config/data directory this app uses
// for its config.json and (only in the keychain-unavailable fallback path)
// its 0600 token file(s).
const appDataDirName = "onlinemenu-pos-desktop"

// hardwarePrinterEvent is the Wails event topic the frontend subscribes to
// for printer connectivity updates (see runtime.EventsOn in the frontend).
const hardwarePrinterEvent = "hardware:printer"

// keycloakClientID is the "pos-desktop" public client registered in
// deploy/keycloak/realm-onlinemenu.json (Authorization Code + PKCE S256,
// RFC 8252 loopback redirect — see internal/keycloakauth).
const keycloakClientID = "pos-desktop"

// keycloakLoginTimeout bounds how long LoginWithKeycloak waits for the
// loopback callback before giving up (see keycloakauth.LoopbackServer.Wait)
// — the task brief's "2 dk içinde callback gelmezse iptal".
const keycloakLoginTimeout = 2 * time.Minute

// kcAccessExpirySkew mirrors the admin frontend's EXPIRY_SKEW_MS
// (keycloak-token-store.ts): treat the in-memory Keycloak access token as
// expired this long before its actual expiry, to avoid a request racing
// the token's last valid instant.
const kcAccessExpirySkew = 10 * time.Second

// App is the Wails-bound application struct. Every method with a pointer
// receiver and an exported name becomes a callable binding in the
// frontend's generated wailsjs/go/main/App bindings.
//
// App — not the frontend — is the sole HTTP and token authority for this
// process (see internal/apiclient doc comment and pos-desktop/README.md,
// "Tek token-refresh otoritesi").
type App struct {
	ctx context.Context

	api *apiclient.Client

	hardwareCancel context.CancelFunc
	printer        *hardware.MockPrinter

	// Keycloak login flow (Sprint-6 Wave 3) — see
	// internal/keycloakauth and pos-desktop/README.md, "Keychain
	// içeriği". kc talks to the Keycloak IdP only; kcStore persists
	// ONLY {refresh_token, membership_id} (keycloakauth.SessionState) —
	// never an access/ID/CTX token.
	kc      *keycloakauth.Client
	kcStore tokenstore.Store

	// openURL opens url in the system browser — defaults (set in startup)
	// to runtime.BrowserOpenURL, injectable so app_test.go can drive
	// LoginWithKeycloak/Logout end-to-end against httptest fakes without a
	// real Wails runtime context (runtime.BrowserOpenURL panics outside
	// one — see runtime.getFrontend). This is the only Wails-runtime call
	// on LoginWithKeycloak's happy/error paths (LogWarning calls only fire
	// on already-degraded branches — see persistKeycloakRefresh callers),
	// so injecting just this one seam is enough to make the whole
	// orchestration method testable.
	openURL func(url string)

	// kcMu guards the in-memory-only Keycloak session fields below. They
	// are never persisted (see kcStore doc above); Wails invokes each
	// bound method call on its own goroutine (see apiclient.Client's doc
	// comment for the same caveat), so concurrent LoginWithKeycloak /
	// SelectKeycloakContext / TryRestoreSession / Logout calls are
	// possible and must not race.
	kcMu           sync.Mutex
	kcAccessToken  string
	kcAccessExpiry time.Time
	kcMembershipID string

	// enableDevLogin mirrors config.Config.EnableDevLogin (POS_ENABLE_DEV_LOGIN)
	// — exposed to the frontend via DevLoginEnabled so the dev-login form
	// can hide itself outside dev/staging, the same way admin's
	// NEXT_PUBLIC_ENABLE_DEV_LOGIN gates its own dev-login form.
	enableDevLogin bool
}

// NewApp creates a new App application struct. Heavy initialization
// (config load, token store, HTTP client) happens in startup, once a Wails
// runtime context is available for logging and event emission.
func NewApp() *App {
	return &App{}
}

// startup is called once by the Wails runtime when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	dataDir, err := userDataDir()
	if err != nil {
		runtime.LogError(ctx, "resolve user data dir: "+err.Error())
		dataDir = "."
	}

	cfg, err := config.Load(dataDir)
	if err != nil {
		runtime.LogError(ctx, "load config: "+err.Error())
	}

	warn := func(format string, args ...any) {
		runtime.LogWarning(ctx, fmt.Sprintf(format, args...))
	}

	store := tokenstore.New(dataDir, warn)
	a.kcStore = tokenstore.NewKeycloak(dataDir, warn)

	a.api = apiclient.New(cfg.APIBaseURL, store)
	a.kc = keycloakauth.New(keycloakauth.Config{
		BaseURL:  cfg.KeycloakURL,
		Realm:    cfg.KeycloakRealm,
		ClientID: keycloakClientID,
	})
	a.enableDevLogin = cfg.EnableDevLogin
	a.openURL = func(url string) { runtime.BrowserOpenURL(a.ctx, url) }

	a.startHardware(ctx)
}

// DevLoginEnabled reports whether the frontend should offer the POST
// /dev/login shortcut next to the "Keycloak ile giriş" button (see
// config.Config.EnableDevLogin / POS_ENABLE_DEV_LOGIN).
func (a *App) DevLoginEnabled() bool {
	return a.enableDevLogin
}

// shutdown is called once by the Wails runtime when the app is closing. It
// stops the hardware event loop and waits for it to fully exit — the
// station must never leave a background device poller running past
// process shutdown.
func (a *App) shutdown(_ context.Context) {
	if a.hardwareCancel != nil {
		a.hardwareCancel()
	}
	if a.printer != nil {
		a.printer.Wait()
	}
}

// startHardware wires the mock printer's event loop to the frontend. Real
// device adapters (printer, scale, fiscal) implementing hardware.Device
// will be registered the same way in the UI wave; the forwarding pattern
// (Go event channel -> runtime.EventsEmit) does not change per device.
func (a *App) startHardware(ctx context.Context) {
	hwCtx, cancel := context.WithCancel(ctx)
	a.hardwareCancel = cancel

	a.printer = hardware.NewMockPrinter()
	a.printer.Start(hwCtx)

	go func() {
		for evt := range a.printer.Events() {
			dto := hardwareEventDTO{
				Kind:   a.printer.Kind(),
				Status: evt.Status.String(),
			}
			if evt.Err != nil {
				dto.Error = evt.Err.Error()
			}
			runtime.EventsEmit(ctx, hardwarePrinterEvent, dto)
		}
	}()
}

// hardwareEventDTO is the JSON shape emitted to the frontend for every
// hardware status transition. Error is only populated for a StatusError
// transition — its presence in the JSON is the frontend's signal to show
// an explicit fault, not an inferred one.
type hardwareEventDTO struct {
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// SessionDTO is the shape returned to the frontend by Login/WhoAmI. It
// intentionally carries no token — the token never leaves the Go process
// (see internal/apiclient doc comment). BranchID is empty for a chain-wide
// staff session (see apiclient.Client.claims) — the UI must prompt for a
// branch/table selection rather than assume one.
type SessionDTO struct {
	Authenticated bool   `json:"authenticated"`
	TenantID      string `json:"tenant_id,omitempty"`
	BranchID      string `json:"branch_id,omitempty"`
	UserID        string `json:"user_id,omitempty"`
	FullName      string `json:"full_name,omitempty"`
	Email         string `json:"email,omitempty"`
}

// Login authenticates the station against the backend's dev-login flow and
// persists the session token in the OS keychain (or its 0600-file
// fallback). This is a Wails binding — the frontend calls it directly, it
// never issues the underlying HTTP request itself.
func (a *App) Login(email string) (SessionDTO, error) {
	session, err := a.api.Login(a.ctx, email)
	if err != nil {
		return SessionDTO{}, err
	}
	return SessionDTO{
		Authenticated: true,
		TenantID:      session.TenantID,
		BranchID:      session.BranchID,
		UserID:        session.UserID,
		FullName:      session.FullName,
		Email:         session.Email,
	}, nil
}

// WhoAmI confirms the current session token (if any) is valid and returns
// the authenticated identity. Returns Authenticated: false rather than an
// error when no session exists, since "not logged in" is an expected UI
// state, not a failure.
func (a *App) WhoAmI() (SessionDTO, error) {
	if !a.api.IsAuthenticated() {
		return SessionDTO{Authenticated: false}, nil
	}

	session, err := a.api.WhoAmI(a.ctx)
	if err != nil {
		if errors.Is(err, apiclient.ErrUnauthenticated) {
			return SessionDTO{Authenticated: false}, nil
		}
		return SessionDTO{}, err
	}
	return SessionDTO{
		Authenticated: true,
		TenantID:      session.TenantID,
		BranchID:      session.BranchID,
		UserID:        session.UserID,
		FullName:      session.FullName,
		Email:         session.Email,
	}, nil
}

// Logout clears the persisted session (dev-login CTX token, Keycloak
// refresh token + membership) and best-effort opens the Keycloak
// end-session endpoint in the system browser (frontchannel logout — the
// "pos-desktop" client has frontchannelLogout enabled, see
// deploy/keycloak/realm-onlinemenu.json). Failure to open the browser is
// not fatal: the local session is already cleared by the time
// BrowserOpenURL is called.
func (a *App) Logout() error {
	_, loadErr := keycloakauth.LoadSessionState(a.kcStore)
	hadKeycloakSession := loadErr == nil

	a.kcMu.Lock()
	a.kcAccessToken = ""
	a.kcAccessExpiry = time.Time{}
	a.kcMembershipID = ""
	a.kcMu.Unlock()

	a.api.ClearUnauthorizedRecovery()

	if err := a.kcStore.Clear(); err != nil {
		runtime.LogWarning(a.ctx, "clear keycloak session state: "+err.Error())
	}

	if err := a.api.Logout(); err != nil {
		return err
	}

	if hadKeycloakSession {
		// No id_token_hint: the ID token is never retained past the login
		// flow's nonce check (see LoginWithKeycloak) — Keycloak still
		// clears its own session cookie for the browser without the hint.
		a.openURL(a.kc.EndSessionURL(""))
	}
	return nil
}

// KeycloakContextDTO mirrors apiclient.ContextItem for the frontend's
// context-picker view (shown when a Keycloak-authenticated person has more
// than one selectable tenant/branch membership).
type KeycloakContextDTO struct {
	MembershipID string `json:"membership_id"`
	TenantID     string `json:"tenant_id"`
	TenantName   string `json:"tenant_name"`
	BranchID     string `json:"branch_id,omitempty"`
	BranchName   string `json:"branch_name,omitempty"`
	RoleID       string `json:"role_id"`
	RoleName     string `json:"role_name"`
}

// KeycloakLoginResultDTO is LoginWithKeycloak/TryRestoreSession's return
// shape: either a completed Session (NeedsContextSelection false — the
// single-membership case auto-selects) or a list of Contexts the frontend
// must render a picker for (see SelectKeycloakContext).
type KeycloakLoginResultDTO struct {
	Session               SessionDTO           `json:"session"`
	NeedsContextSelection bool                 `json:"needs_context_selection"`
	Contexts              []KeycloakContextDTO `json:"contexts,omitempty"`
}

// LoginWithKeycloak starts the RFC 8252 loopback-redirect Authorization
// Code + PKCE flow against the "pos-desktop" Keycloak client: it opens the
// system browser at the realm's authorize endpoint, waits (up to
// keycloakLoginTimeout) for the /callback redirect, exchanges the code for
// tokens, persists the refresh token to the keychain, and resolves the
// resulting Keycloak session down to either a completed backend CTX
// session (single membership, auto-selected) or a context list for the
// frontend to render a picker for. This is a Wails binding — the frontend
// calls it directly; it never issues the underlying HTTP/browser calls
// itself (see pos-desktop/README.md, "Tek token-refresh otoritesi").
func (a *App) LoginWithKeycloak() (KeycloakLoginResultDTO, error) {
	verifier, err := keycloakauth.GenerateVerifier()
	if err != nil {
		return KeycloakLoginResultDTO{}, fmt.Errorf("login with keycloak: %w", err)
	}
	state, err := keycloakauth.GenerateState()
	if err != nil {
		return KeycloakLoginResultDTO{}, fmt.Errorf("login with keycloak: %w", err)
	}
	nonce, err := keycloakauth.GenerateNonce()
	if err != nil {
		return KeycloakLoginResultDTO{}, fmt.Errorf("login with keycloak: %w", err)
	}
	challenge := keycloakauth.Challenge(verifier)

	loopback, err := keycloakauth.NewLoopbackServer()
	if err != nil {
		return KeycloakLoginResultDTO{}, fmt.Errorf("login with keycloak: start loopback listener: %w", err)
	}

	authURL := a.kc.AuthorizeURL(keycloakauth.AuthorizeURLParams{
		RedirectURI:   loopback.RedirectURI(),
		State:         state,
		Nonce:         nonce,
		CodeChallenge: challenge,
	})
	a.openURL(authURL)

	result, err := loopback.Wait(a.ctx, keycloakLoginTimeout)
	if err != nil {
		return KeycloakLoginResultDTO{}, fmt.Errorf("login with keycloak: %w", err)
	}
	if result.State != state {
		return KeycloakLoginResultDTO{}, errors.New("login with keycloak: state mismatch — possible CSRF, aborting")
	}

	tokens, err := a.kc.Exchange(a.ctx, result.Code, verifier, loopback.RedirectURI())
	if err != nil {
		return KeycloakLoginResultDTO{}, fmt.Errorf("login with keycloak: %w", err)
	}

	if tokens.IDToken != "" {
		gotNonce, nonceErr := keycloakauth.DecodeNonce(tokens.IDToken)
		if nonceErr != nil || gotNonce != nonce {
			return KeycloakLoginResultDTO{}, errors.New("login with keycloak: id_token nonce mismatch — possible replay, aborting")
		}
	}

	a.setKeycloakAccessToken(tokens.AccessToken, tokens.ExpiresIn)
	if err := a.persistKeycloakRefresh(tokens.RefreshToken, ""); err != nil {
		runtime.LogWarning(a.ctx, "persist keycloak refresh token: "+err.Error())
	}

	return a.resolveContexts(tokens.AccessToken)
}

// SelectKeycloakContext completes a login that returned
// NeedsContextSelection=true (multiple memberships) — the frontend calls
// this with the membership_id the cashier picked. It reuses the Keycloak
// access token obtained by the immediately preceding LoginWithKeycloak or
// TryRestoreSession call, silently refreshing it first if it has expired
// (see currentKeycloakAccessToken) — a station that leaves the picker open
// past the Keycloak refresh token's lifetime as well would need to restart
// the flow, but that is not the expected case (the picker is answered
// promptly).
func (a *App) SelectKeycloakContext(membershipID string) (SessionDTO, error) {
	accessToken, err := a.currentKeycloakAccessToken(a.ctx)
	if err != nil {
		return SessionDTO{}, fmt.Errorf("select keycloak context: %w", err)
	}
	return a.completeContextSelection(accessToken, membershipID)
}

// TryRestoreSession is called once by the frontend on mount, before it
// decides whether to show the login screen. It tries, in order: (1) a
// silent Keycloak refresh-token-backed restore (re-wiring the same CTX-401
// recovery hook a fresh LoginWithKeycloak would — see
// recoverKeycloakContext), and (2) the pre-existing dev-login path (a CTX
// token the dev-login flow persisted directly to the keychain — see
// apiclient.New's eager load). Trying Keycloak first also overrides any
// stale dev-login CTX token apiclient.New eagerly loaded, so there is no
// ambiguity about which session wins when a station has used both flows.
// Returns Authenticated:false, nil (not an error) if neither restores a
// session — "not logged in" is expected UI state, not a failure.
func (a *App) TryRestoreSession() (SessionDTO, error) {
	if session, ok := a.tryRestoreKeycloakSession(); ok {
		return session, nil
	}
	return a.WhoAmI()
}

// tryRestoreKeycloakSession attempts the Keycloak-backed silent restore.
// Every failure path degrades to (SessionDTO{}, false) rather than an
// error — per the task brief ("başarısızsa login ekranı atlanmaz"), a
// failed silent restore must fall through to the login screen, not
// surface as an error dialog.
func (a *App) tryRestoreKeycloakSession() (SessionDTO, bool) {
	state, err := keycloakauth.LoadSessionState(a.kcStore)
	if err != nil {
		return SessionDTO{}, false
	}

	tokens, err := a.kc.Refresh(a.ctx, state.RefreshToken)
	if err != nil {
		runtime.LogWarning(a.ctx, "keycloak silent refresh failed, falling back to login screen: "+err.Error())
		return SessionDTO{}, false
	}
	a.setKeycloakAccessToken(tokens.AccessToken, tokens.ExpiresIn)

	membershipID := state.MembershipID
	if membershipID == "" {
		// Refresh succeeded but no membership was ever recorded — e.g. the
		// process was killed between LoginWithKeycloak's context fetch and
		// the picker's answer. Persist the rotated refresh token and fall
		// through to the login screen; the frontend starts a fresh
		// LoginWithKeycloak, which re-resolves the picker.
		if err := a.persistKeycloakRefresh(tokens.RefreshToken, ""); err != nil {
			runtime.LogWarning(a.ctx, "persist rotated keycloak refresh token: "+err.Error())
		}
		return SessionDTO{}, false
	}

	if err := a.persistKeycloakRefresh(tokens.RefreshToken, membershipID); err != nil {
		runtime.LogWarning(a.ctx, "persist rotated keycloak refresh token: "+err.Error())
	}

	session, err := a.completeContextSelection(tokens.AccessToken, membershipID)
	if err != nil {
		runtime.LogWarning(a.ctx, "keycloak context restore failed, falling back to login screen: "+err.Error())
		return SessionDTO{}, false
	}
	return session, true
}

// resolveContexts fetches the memberships available to accessToken and
// either auto-selects the single one or returns the list for the frontend
// to render a picker for.
func (a *App) resolveContexts(accessToken string) (KeycloakLoginResultDTO, error) {
	items, err := a.api.FetchKeycloakContexts(a.ctx, accessToken)
	if err != nil {
		return KeycloakLoginResultDTO{}, fmt.Errorf("login with keycloak: fetch contexts: %w", err)
	}
	if len(items) == 0 {
		return KeycloakLoginResultDTO{}, errors.New("login with keycloak: no tenant/branch membership found for this account")
	}
	if len(items) == 1 {
		session, err := a.completeContextSelection(accessToken, items[0].MembershipID)
		if err != nil {
			return KeycloakLoginResultDTO{}, err
		}
		return KeycloakLoginResultDTO{Session: session}, nil
	}

	dtos := make([]KeycloakContextDTO, len(items))
	for i, item := range items {
		dtos[i] = KeycloakContextDTO{
			MembershipID: item.MembershipID,
			TenantID:     item.TenantID,
			TenantName:   item.TenantName,
			BranchID:     item.BranchID,
			BranchName:   item.BranchName,
			RoleID:       item.RoleID,
			RoleName:     item.RoleName,
		}
	}
	return KeycloakLoginResultDTO{NeedsContextSelection: true, Contexts: dtos}, nil
}

// completeContextSelection selects membershipID with accessToken, installs
// the resulting CTX token (in-memory only — see apiclient.Client.SetSessionToken),
// wires the CTX-401 recovery hook, persists the membership choice, and
// returns the resulting Session via WhoAmI.
func (a *App) completeContextSelection(accessToken, membershipID string) (SessionDTO, error) {
	ctxToken, err := a.api.SelectKeycloakContext(a.ctx, accessToken, membershipID)
	if err != nil {
		return SessionDTO{}, fmt.Errorf("select context: %w", err)
	}
	a.api.SetSessionToken(ctxToken)
	a.api.SetUnauthorizedRecovery(a.recoverKeycloakContext)

	if err := a.persistKeycloakMembership(membershipID); err != nil {
		runtime.LogWarning(a.ctx, "persist keycloak membership: "+err.Error())
	}

	session, err := a.api.WhoAmI(a.ctx)
	if err != nil {
		return SessionDTO{}, fmt.Errorf("whoami after context selection: %w", err)
	}
	return SessionDTO{
		Authenticated: true,
		TenantID:      session.TenantID,
		BranchID:      session.BranchID,
		UserID:        session.UserID,
		FullName:      session.FullName,
		Email:         session.Email,
	}, nil
}

// recoverKeycloakContext is apiclient.Client's CTX-401 recovery hook (see
// SetUnauthorizedRecovery), wired only after a Keycloak-derived context
// selection — never for the dev-login flow. Mirrors admin's
// recoverCtxToken (lib/api.ts): re-derive a fresh CTX token from the
// still-valid (or silently refreshed) Keycloak session and the
// last-selected membership, without sending the cashier back through the
// context picker. apiclient.Client already single-flights concurrent
// callers of this hook (see Client.recoverToken).
//
// This must only call apiclient methods that bypass 401 recovery
// (SelectKeycloakContext uses doWithBearer) — routing back through
// do/doWithHeaders here would recurse.
func (a *App) recoverKeycloakContext(ctx context.Context) (string, error) {
	a.kcMu.Lock()
	membershipID := a.kcMembershipID
	a.kcMu.Unlock()
	if membershipID == "" {
		return "", errors.New("recover keycloak context: no membership on record")
	}

	accessToken, err := a.currentKeycloakAccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("recover keycloak context: %w", err)
	}
	return a.api.SelectKeycloakContext(ctx, accessToken, membershipID)
}

// currentKeycloakAccessToken returns the in-memory Keycloak access token if
// it is still valid, silently refreshing it from the persisted refresh
// token otherwise.
func (a *App) currentKeycloakAccessToken(ctx context.Context) (string, error) {
	a.kcMu.Lock()
	tok, expiry := a.kcAccessToken, a.kcAccessExpiry
	a.kcMu.Unlock()

	if tok != "" && time.Now().Before(expiry.Add(-kcAccessExpirySkew)) {
		return tok, nil
	}

	state, err := keycloakauth.LoadSessionState(a.kcStore)
	if err != nil {
		return "", fmt.Errorf("no keycloak session to refresh from: %w", err)
	}
	tokens, err := a.kc.Refresh(ctx, state.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("refresh keycloak access token: %w", err)
	}
	a.setKeycloakAccessToken(tokens.AccessToken, tokens.ExpiresIn)
	if err := a.persistKeycloakRefresh(tokens.RefreshToken, state.MembershipID); err != nil {
		runtime.LogWarning(a.ctx, "persist rotated keycloak refresh token: "+err.Error())
	}
	return tokens.AccessToken, nil
}

func (a *App) setKeycloakAccessToken(accessToken string, expiresIn int) {
	a.kcMu.Lock()
	a.kcAccessToken = accessToken
	a.kcAccessExpiry = time.Now().Add(time.Duration(expiresIn) * time.Second)
	a.kcMu.Unlock()
}

// persistKeycloakRefresh persists a (possibly rotated) refresh token. When
// membershipID is "" the existing persisted membership (if any) is
// preserved rather than wiped — refresh-token rotation and membership
// selection are independent events that both write into the same combined
// keychain blob (see keycloakauth.SessionState).
func (a *App) persistKeycloakRefresh(refreshToken, membershipID string) error {
	if membershipID == "" {
		if existing, err := keycloakauth.LoadSessionState(a.kcStore); err == nil {
			membershipID = existing.MembershipID
		}
	}
	a.kcMu.Lock()
	a.kcMembershipID = membershipID
	a.kcMu.Unlock()
	return keycloakauth.SaveSessionState(a.kcStore, keycloakauth.SessionState{
		RefreshToken: refreshToken,
		MembershipID: membershipID,
	})
}

// persistKeycloakMembership persists a newly-selected membership_id,
// preserving whatever refresh token is already on record.
func (a *App) persistKeycloakMembership(membershipID string) error {
	refreshToken := ""
	if existing, err := keycloakauth.LoadSessionState(a.kcStore); err == nil {
		refreshToken = existing.RefreshToken
	}
	a.kcMu.Lock()
	a.kcMembershipID = membershipID
	a.kcMu.Unlock()
	return keycloakauth.SaveSessionState(a.kcStore, keycloakauth.SessionState{
		RefreshToken: refreshToken,
		MembershipID: membershipID,
	})
}

// Ping checks backend reachability (GET /healthz) without requiring
// authentication. Returns nil on success; the frontend renders the error
// string on failure.
func (a *App) Ping() error {
	return a.api.Ping(a.ctx)
}

func userDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDataDirName), nil
}
