package keycloakauth

import (
	"encoding/json"
	"errors"
	"fmt"

	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// SessionState is the only thing the Keycloak login flow persists to the OS
// keychain: the refresh token (so the station can silently re-authenticate
// on restart — see main.App.TryRestoreSession) and the last-selected
// membership_id (so a multi-membership cashier is not sent back through the
// context picker on every restart). Access and ID tokens are NEVER part of
// this blob — they live in memory only, for the process's lifetime (see
// pos-desktop/README.md, "Keychain içeriği").
type SessionState struct {
	RefreshToken string `json:"refresh_token"`
	MembershipID string `json:"membership_id"`
}

// ErrNoSession is returned by LoadSessionState when nothing has been
// persisted yet (no prior successful Keycloak login on this station).
var ErrNoSession = errors.New("keycloakauth: no session state stored")

// SaveSessionState marshals state and persists it via store — expected to
// be the tokenstore.Store instance returned by tokenstore.NewKeycloak, kept
// separate from the dev-login flow's "session-token" store (see
// tokenstore.New) so the two flows' keychain entries never collide.
func SaveSessionState(store tokenstore.Store, state SessionState) error {
	data, err := json.Marshal(state) // #nosec G117 -- deliberately marshaling refresh_token for OS keychain persistence (this function's entire purpose, see doc comment above); not a log/leak path.
	if err != nil {
		return fmt.Errorf("keycloakauth: marshal session state: %w", err)
	}
	if err := store.Save(string(data)); err != nil {
		return fmt.Errorf("keycloakauth: persist session state: %w", err)
	}
	return nil
}

// LoadSessionState reads and unmarshals the persisted state, or
// ErrNoSession if nothing has been saved yet.
func LoadSessionState(store tokenstore.Store) (SessionState, error) {
	raw, err := store.Load()
	if err != nil {
		if errors.Is(err, tokenstore.ErrNoToken) {
			return SessionState{}, ErrNoSession
		}
		return SessionState{}, fmt.Errorf("keycloakauth: load session state: %w", err)
	}
	var state SessionState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return SessionState{}, fmt.Errorf("keycloakauth: unmarshal session state: %w", err)
	}
	return state, nil
}
