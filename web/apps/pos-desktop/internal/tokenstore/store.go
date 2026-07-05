// Package tokenstore persists the POS station's session token.
//
// Per lessons-from-b2b Bölüm 5, tokens must never be written to plaintext
// config. The OS keychain (macOS Keychain / Windows Credential Manager /
// Linux Secret Service via go-keyring) is the primary store. When no
// keychain backend is available (e.g. a headless Linux kiosk without a
// Secret Service provider), the store falls back to a 0600 file and emits
// an explicit warning through the caller-supplied Warn func — it never
// fails silently.
package tokenstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

// service and user identify the credential entry in the OS keychain. A
// single POS station holds one active session at a time, so a fixed
// account name is sufficient.
const (
	service = "onlinemenu.tr-pos-desktop"
	account = "session-token"
)

// ErrNoToken is returned by Load when no token has been persisted yet.
var ErrNoToken = errors.New("tokenstore: no token stored")

// Store persists and retrieves the current session token.
type Store interface {
	Save(token string) error
	Load() (string, error)
	Clear() error
}

// Warn reports a non-fatal condition (e.g. keychain unavailable) to the
// caller. Implementations must not swallow this — it is surfaced to
// application logs so operators know a station is running in degraded
// (file-based) token storage mode.
type Warn func(format string, args ...any)

// New selects the OS keychain when available, otherwise falls back to a
// 0600 file under dataDir. Availability is probed with a real round-trip
// (write, read, delete) rather than trusting a static OS check, since
// keychain daemons can be installed but non-functional (e.g. locked,
// missing Secret Service on Linux).
func New(dataDir string, warn Warn) Store {
	if warn == nil {
		warn = func(string, ...any) {}
	}

	ks := &keyringStore{}
	if err := ks.probe(); err != nil {
		warn("tokenstore: OS keychain unavailable (%v); falling back to encrypted-at-rest-less 0600 file store at %s — this is a degraded security mode", err, dataDir)
		return &fileStore{path: filepath.Join(dataDir, "session.token")}
	}
	return ks
}

// keyringStore stores the token in the OS-native credential manager.
type keyringStore struct{}

func (s *keyringStore) probe() error {
	const probeValue = "probe"
	if err := keyring.Set(service, account+"-probe", probeValue); err != nil {
		return fmt.Errorf("keyring probe set: %w", err)
	}
	defer func() { _ = keyring.Delete(service, account+"-probe") }()

	got, err := keyring.Get(service, account+"-probe")
	if err != nil {
		return fmt.Errorf("keyring probe get: %w", err)
	}
	if got != probeValue {
		return fmt.Errorf("keyring probe mismatch")
	}
	return nil
}

func (s *keyringStore) Save(token string) error {
	if err := keyring.Set(service, account, token); err != nil {
		return fmt.Errorf("tokenstore: keyring save: %w", err)
	}
	return nil
}

func (s *keyringStore) Load() (string, error) {
	token, err := keyring.Get(service, account)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNoToken
		}
		return "", fmt.Errorf("tokenstore: keyring load: %w", err)
	}
	return token, nil
}

func (s *keyringStore) Clear() error {
	if err := keyring.Delete(service, account); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("tokenstore: keyring clear: %w", err)
	}
	return nil
}

// fileStore is the degraded fallback used only when the OS keychain is
// unavailable. The file is created with 0600 permissions (owner read/write
// only); this is a fallback, not a substitute for the keychain.
type fileStore struct {
	path string
}

func (s *fileStore) Save(token string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("tokenstore: create dir: %w", err)
	}
	if err := os.WriteFile(s.path, []byte(token), 0o600); err != nil {
		return fmt.Errorf("tokenstore: write file: %w", err)
	}
	return nil
}

func (s *fileStore) Load() (string, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNoToken
		}
		return "", fmt.Errorf("tokenstore: read file: %w", err)
	}
	if len(data) == 0 {
		return "", ErrNoToken
	}
	return string(data), nil
}

func (s *fileStore) Clear() error {
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("tokenstore: remove file: %w", err)
	}
	return nil
}
