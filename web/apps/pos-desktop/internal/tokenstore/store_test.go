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
