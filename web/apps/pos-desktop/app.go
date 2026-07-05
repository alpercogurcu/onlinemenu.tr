package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
	"onlinemenu.tr/pos-desktop/internal/config"
	"onlinemenu.tr/pos-desktop/internal/hardware"
	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// appDataDirName names the per-OS user config/data directory this app uses
// for its config.json and (only in the keychain-unavailable fallback path)
// its 0600 token file.
const appDataDirName = "onlinemenu-pos-desktop"

// hardwarePrinterEvent is the Wails event topic the frontend subscribes to
// for printer connectivity updates (see runtime.EventsOn in the frontend).
const hardwarePrinterEvent = "hardware:printer"

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

	store := tokenstore.New(dataDir, func(format string, args ...any) {
		runtime.LogWarning(ctx, fmt.Sprintf(format, args...))
	})

	a.api = apiclient.New(cfg.APIBaseURL, store)

	a.startHardware(ctx)
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
// (see internal/apiclient doc comment).
type SessionDTO struct {
	Authenticated bool   `json:"authenticated"`
	TenantID      string `json:"tenant_id,omitempty"`
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
		UserID:        session.UserID,
		FullName:      session.FullName,
		Email:         session.Email,
	}, nil
}

// Logout clears the persisted session token.
func (a *App) Logout() error {
	return a.api.Logout()
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
