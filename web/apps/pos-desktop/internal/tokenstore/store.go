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

// service identifies this app's credential entries in the OS keychain.
// Two distinct accounts live under it — sessionAccount for the dev-login
// CTX token (New) and keycloakAccount for the Keycloak refresh-token +
// membership-id blob (NewKeycloak, see internal/keycloakauth) — so the two
// login flows' keychain entries never collide. A single POS station holds
// at most one active session per flow at a time, so fixed account names
// are sufficient.
const (
	service        = "onlinemenu.tr-pos-desktop"
	sessionAccount = "session-token"
)

// ErrNoToken is returned by Load when no token has been persisted yet.
var ErrNoToken = errors.New("tokenstore: no token stored")

// Store persists and retrieves a single opaque string value (the dev-login
// CTX token for New, or a JSON-encoded keycloakauth.SessionState for
// NewKeycloak).
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
// 0600 file under dataDir, for the dev-login flow's session token.
// Availability is probed with a real round-trip (write, read, delete)
// rather than trusting a static OS check, since keychain daemons can be
// installed but non-functional (e.g. locked, missing Secret Service on
// Linux).
func New(dataDir string, warn Warn) Store {
	return newStore(dataDir, sessionAccount, "session.token", warn)
}

// keycloakAccount and its file fallback name are deliberately distinct from
// sessionAccount/"session.token" above.
const keycloakAccount = "keycloak-refresh"

// NewKeycloak is New's counterpart for the Keycloak login flow: it persists
// only a keycloakauth.SessionState blob (refresh_token + membership_id),
// never the dev-login CTX token, under its own keychain account / fallback
// file so the two flows never overwrite each other.
func NewKeycloak(dataDir string, warn Warn) Store {
	return newStore(dataDir, keycloakAccount, "keycloak-refresh.token", warn)
}

func newStore(dataDir, account, fallbackFilename string, warn Warn) Store {
	if warn == nil {
		warn = func(string, ...any) {}
	}

	ks := &keyringStore{account: account}
	if err := ks.probe(); err != nil {
		warn("tokenstore: OS keychain unavailable (%v); falling back to encrypted-at-rest-less 0600 file store at %s — this is a degraded security mode", err, dataDir)
		return &fileStore{path: filepath.Join(dataDir, fallbackFilename)}
	}
	return ks
}

// keyringStore stores the token in the OS-native credential manager, under
// its own account name (see New/NewKeycloak).
type keyringStore struct {
	account string
}

func (s *keyringStore) probe() error {
	const probeValue = "probe"
	probeAccount := s.account + "-probe"
	if err := keyring.Set(service, probeAccount, probeValue); err != nil {
		return fmt.Errorf("keyring probe set: %w", err)
	}
	defer func() { _ = keyring.Delete(service, probeAccount) }()

	got, err := keyring.Get(service, probeAccount)
	if err != nil {
		return fmt.Errorf("keyring probe get: %w", err)
	}
	if got != probeValue {
		return fmt.Errorf("keyring probe mismatch")
	}
	return nil
}

func (s *keyringStore) Save(token string) error {
	if err := keyring.Set(service, s.account, token); err != nil {
		return fmt.Errorf("tokenstore: keyring save: %w", err)
	}
	return nil
}

func (s *keyringStore) Load() (string, error) {
	token, err := keyring.Get(service, s.account)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNoToken
		}
		return "", fmt.Errorf("tokenstore: keyring load: %w", err)
	}
	return token, nil
}

func (s *keyringStore) Clear() error {
	if err := keyring.Delete(service, s.account); err != nil {
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
