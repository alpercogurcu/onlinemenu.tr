package keycloakauth

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"

	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

func TestSaveAndLoadSessionState_RoundTrip(t *testing.T) {
	keyring.MockInit()
	store := tokenstore.NewKeycloak(t.TempDir(), nil)

	want := SessionState{RefreshToken: "rt-1", MembershipID: "membership-1"}
	if err := SaveSessionState(store, want); err != nil {
		t.Fatalf("SaveSessionState: %v", err)
	}

	got, err := LoadSessionState(store)
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if got != want {
		t.Fatalf("LoadSessionState = %+v, want %+v", got, want)
	}
}

func TestLoadSessionState_NoneStoredReturnsErrNoSession(t *testing.T) {
	keyring.MockInit()
	store := tokenstore.NewKeycloak(t.TempDir(), nil)

	_, err := LoadSessionState(store)
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("LoadSessionState = %v, want ErrNoSession", err)
	}
}

func TestSaveSessionState_OverwritesPreviousMembership(t *testing.T) {
	keyring.MockInit()
	store := tokenstore.NewKeycloak(t.TempDir(), nil)

	if err := SaveSessionState(store, SessionState{RefreshToken: "rt-1", MembershipID: "m1"}); err != nil {
		t.Fatalf("SaveSessionState: %v", err)
	}
	if err := SaveSessionState(store, SessionState{RefreshToken: "rt-2", MembershipID: "m2"}); err != nil {
		t.Fatalf("SaveSessionState: %v", err)
	}

	got, err := LoadSessionState(store)
	if err != nil {
		t.Fatalf("LoadSessionState: %v", err)
	}
	if got.RefreshToken != "rt-2" || got.MembershipID != "m2" {
		t.Fatalf("LoadSessionState = %+v, want rotated rt-2/m2", got)
	}
}
