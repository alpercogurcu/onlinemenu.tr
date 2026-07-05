package tokenstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestNew_UsesKeyringWhenAvailable(t *testing.T) {
	keyring.MockInit()

	s := New(t.TempDir(), func(string, ...any) { t.Fatal("warn should not fire when keyring is available") })
	if _, ok := s.(*keyringStore); !ok {
		t.Fatalf("expected keyringStore, got %T", s)
	}
}

func TestKeyringStore_SaveLoadClearRoundTrip(t *testing.T) {
	keyring.MockInit()

	s := New(t.TempDir(), nil)

	if err := s.Save("ctx-token-abc"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "ctx-token-abc" {
		t.Fatalf("Load: got %q, want %q", got, "ctx-token-abc")
	}

	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNoToken) {
		t.Fatalf("Load after Clear: got err %v, want ErrNoToken", err)
	}
}

func TestFileStore_FallbackWhenKeyringUnavailable(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service on this host"))

	var warned bool
	dir := t.TempDir()
	s := New(dir, func(format string, args ...any) { warned = true })

	if _, ok := s.(*fileStore); !ok {
		t.Fatalf("expected fileStore fallback, got %T", s)
	}
	if !warned {
		t.Fatal("expected Warn to be called when falling back to file store")
	}

	if err := s.Save("fallback-token"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "session.token"))
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file perm = %o, want 0600", perm)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "fallback-token" {
		t.Fatalf("Load: got %q, want %q", got, "fallback-token")
	}

	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNoToken) {
		t.Fatalf("Load after Clear: got err %v, want ErrNoToken", err)
	}
}

func TestFileStore_LoadMissingFileReturnsErrNoToken(t *testing.T) {
	s := &fileStore{path: filepath.Join(t.TempDir(), "does-not-exist.token")}
	if _, err := s.Load(); !errors.Is(err, ErrNoToken) {
		t.Fatalf("got %v, want ErrNoToken", err)
	}
}

// TestNewKeycloak_UsesDistinctAccountFromNew guards the regression this
// second store instance exists to prevent: a dev-login session and a
// Keycloak session must never overwrite each other's keychain entry, since
// both stores share the same service name (see store.go's service const).
func TestNewKeycloak_UsesDistinctAccountFromNew(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()

	sessionStore := New(dir, nil)
	keycloakStore := NewKeycloak(dir, nil)

	if err := sessionStore.Save("dev-login-ctx-token"); err != nil {
		t.Fatalf("sessionStore.Save: %v", err)
	}
	if err := keycloakStore.Save(`{"refresh_token":"rt","membership_id":"m1"}`); err != nil {
		t.Fatalf("keycloakStore.Save: %v", err)
	}

	gotSession, err := sessionStore.Load()
	if err != nil {
		t.Fatalf("sessionStore.Load: %v", err)
	}
	if gotSession != "dev-login-ctx-token" {
		t.Fatalf("sessionStore.Load = %q, want unaffected by keycloakStore.Save", gotSession)
	}

	gotKeycloak, err := keycloakStore.Load()
	if err != nil {
		t.Fatalf("keycloakStore.Load: %v", err)
	}
	if gotKeycloak != `{"refresh_token":"rt","membership_id":"m1"}` {
		t.Fatalf("keycloakStore.Load = %q, unexpected", gotKeycloak)
	}

	if err := keycloakStore.Clear(); err != nil {
		t.Fatalf("keycloakStore.Clear: %v", err)
	}
	if _, err := keycloakStore.Load(); !errors.Is(err, ErrNoToken) {
		t.Fatalf("keycloakStore.Load after Clear: got %v, want ErrNoToken", err)
	}
	// Clearing the keycloak store must not touch the session store.
	if gotSession, err := sessionStore.Load(); err != nil || gotSession != "dev-login-ctx-token" {
		t.Fatalf("sessionStore affected by keycloakStore.Clear: token=%q err=%v", gotSession, err)
	}
}

func TestNewKeycloak_FileFallbackUsesDistinctFilename(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service on this host"))

	dir := t.TempDir()
	s := NewKeycloak(dir, func(string, ...any) {})

	if _, ok := s.(*fileStore); !ok {
		t.Fatalf("expected fileStore fallback, got %T", s)
	}
	if err := s.Save("blob"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "keycloak-refresh.token")); err != nil {
		t.Fatalf("stat keycloak-refresh.token: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "session.token")); !os.IsNotExist(err) {
		t.Fatalf("session.token should not be created by NewKeycloak, stat err = %v", err)
	}
}
