package keycloak_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/platform/keycloak"
)

// fakeKeycloakServer is a minimal in-process stand-in for a Keycloak realm's
// token endpoint + Admin REST API, used so these tests never require a live
// Keycloak instance.
type fakeKeycloakServer struct {
	srv *httptest.Server

	tokenCalls int32

	users        map[string]keycloak.User // keyed by email
	nextID       int
	createStatus int // override for the next CreateUser response; 0 = default 201
	failNotify   bool
}

func newFakeKeycloakServer() *fakeKeycloakServer {
	f := &fakeKeycloakServer{users: map[string]keycloak.User{}}
	mux := http.NewServeMux()

	mux.HandleFunc("/realms/onlinemenu/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.tokenCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-token",
			"expires_in":   3600,
		})
	})

	mux.HandleFunc("/admin/realms/onlinemenu/users", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			email := r.URL.Query().Get("email")
			if u, ok := f.users[email]; ok {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]map[string]any{
					{"id": u.ID, "username": u.Username, "email": u.Email, "enabled": u.Enabled},
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case http.MethodPost:
			var body struct {
				Email string `json:"email"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)

			if f.createStatus != 0 {
				w.WriteHeader(f.createStatus)
				return
			}
			if _, exists := f.users[body.Email]; exists {
				w.WriteHeader(http.StatusConflict)
				return
			}
			f.nextID++
			id := fmt.Sprintf("kc-user-%d", f.nextID)
			f.users[body.Email] = keycloak.User{ID: id, Username: body.Email, Email: body.Email, Enabled: true}
			w.Header().Set("Location", r.URL.String()+"/"+id)
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/admin/realms/onlinemenu/users/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// GET .../users/{id}: GetUserByID. Distinguished from the
			// execute-actions-email PUT below by method, not path, since both
			// share the "/users/" prefix.
			id := strings.TrimPrefix(r.URL.Path, "/admin/realms/onlinemenu/users/")
			for _, u := range f.users {
				if u.ID == id {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"id": u.ID, "username": u.Username, "email": u.Email, "enabled": u.Enabled,
					})
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if f.failNotify {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("SMTP not configured"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	f.srv = httptest.NewServer(mux)
	return f
}

func (f *fakeKeycloakServer) client() *keycloak.Client {
	return keycloak.NewClient(keycloak.Config{
		BaseURL:      f.srv.URL,
		Realm:        "onlinemenu",
		ClientID:     "staff-invite-svc",
		ClientSecret: "secret",
	})
}

func TestClient_FindUserByEmail_NotFound(t *testing.T) {
	f := newFakeKeycloakServer()
	defer f.srv.Close()

	_, found, err := f.client().FindUserByEmail(context.Background(), "nobody@example.com")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestClient_CreateUser_ThenFindByEmail(t *testing.T) {
	f := newFakeKeycloakServer()
	defer f.srv.Close()
	c := f.client()
	ctx := context.Background()

	created, err := c.CreateUser(ctx, keycloak.CreateUserRequest{Email: "ada@example.com", FullName: "Ada Lovelace"})
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, "ada@example.com", created.Email)

	found, ok, err := c.FindUserByEmail(ctx, "ada@example.com")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, created.ID, found.ID)
}

// TestClient_CreateUser_Conflict_ReturnsErrUserAlreadyExists proves the race
// window the ADR calls out: a concurrent invite (or a retry racing itself)
// creating the same user must surface as a distinguished, recoverable error,
// not a generic failure — the service layer recovers by calling
// FindUserByEmail again rather than creating a second Keycloak user.
func TestClient_CreateUser_Conflict_ReturnsErrUserAlreadyExists(t *testing.T) {
	f := newFakeKeycloakServer()
	defer f.srv.Close()
	c := f.client()
	ctx := context.Background()

	_, err := c.CreateUser(ctx, keycloak.CreateUserRequest{Email: "dup@example.com", FullName: "Dup User"})
	require.NoError(t, err)

	_, err = c.CreateUser(ctx, keycloak.CreateUserRequest{Email: "dup@example.com", FullName: "Dup User"})
	require.ErrorIs(t, err, keycloak.ErrUserAlreadyExists)
}

func TestClient_TriggerPasswordSetup_Success(t *testing.T) {
	f := newFakeKeycloakServer()
	defer f.srv.Close()
	c := f.client()
	ctx := context.Background()

	created, err := c.CreateUser(ctx, keycloak.CreateUserRequest{Email: "notify@example.com", FullName: "N Otify"})
	require.NoError(t, err)

	err = c.TriggerPasswordSetup(ctx, created.ID)
	assert.NoError(t, err)
}

// TestClient_TriggerPasswordSetup_Failure_IsSurfaced proves an SMTP-less
// realm's failure to send the password-setup email comes back as a real
// error the caller can inspect, per docs/lessons-from-b2b.md's silent-failure
// ban — it must never be reported as success.
func TestClient_TriggerPasswordSetup_Failure_IsSurfaced(t *testing.T) {
	f := newFakeKeycloakServer()
	f.failNotify = true
	defer f.srv.Close()
	c := f.client()
	ctx := context.Background()

	err := c.TriggerPasswordSetup(ctx, "some-user-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trigger password setup")
}

// TestClient_GetUserByID_Found proves R1's reuse path can resolve a
// persons.keycloak_sub back to its live Keycloak account.
func TestClient_GetUserByID_Found(t *testing.T) {
	f := newFakeKeycloakServer()
	defer f.srv.Close()
	c := f.client()
	ctx := context.Background()

	created, err := c.CreateUser(ctx, keycloak.CreateUserRequest{Email: "lookup@example.com", FullName: "Lookup Me"})
	require.NoError(t, err)

	found, ok, err := c.GetUserByID(ctx, created.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, "lookup@example.com", found.Email)
}

// TestClient_GetUserByID_NotFound proves a 404 (e.g. the account was deleted
// directly in Keycloak) reports found=false with no error, matching
// FindUserByEmail's contract.
func TestClient_GetUserByID_NotFound(t *testing.T) {
	f := newFakeKeycloakServer()
	defer f.srv.Close()
	c := f.client()
	ctx := context.Background()

	_, found, err := c.GetUserByID(ctx, "no-such-user-id")
	require.NoError(t, err)
	assert.False(t, found)
}

// TestClient_TokenIsCachedAcrossCalls proves the client_credentials grant is
// not re-fetched on every Admin API call.
func TestClient_TokenIsCachedAcrossCalls(t *testing.T) {
	f := newFakeKeycloakServer()
	defer f.srv.Close()
	c := f.client()
	ctx := context.Background()

	for range 5 {
		_, _, err := c.FindUserByEmail(ctx, "whoever@example.com")
		require.NoError(t, err)
	}

	assert.Equal(t, int32(1), atomic.LoadInt32(&f.tokenCalls),
		"expected exactly one token fetch across 5 admin calls")
}

// TestClient_TokenRefetchedAfterExpiry proves an expired token is not reused.
func TestClient_TokenRefetchedAfterExpiry(t *testing.T) {
	f := newFakeKeycloakServer()
	defer f.srv.Close()

	now := time.Now().UTC()
	c := keycloak.NewClient(keycloak.Config{
		BaseURL: f.srv.URL, Realm: "onlinemenu", ClientID: "svc", ClientSecret: "secret",
	}, keycloak.WithClock(func() time.Time { return now }))
	ctx := context.Background()

	_, _, err := c.FindUserByEmail(ctx, "a@example.com")
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&f.tokenCalls))

	now = now.Add(2 * time.Hour) // past the 3600s expires_in the fake server returns

	_, _, err = c.FindUserByEmail(ctx, "a@example.com")
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(&f.tokenCalls), "expired token must trigger a re-fetch")
}

func TestClient_NotConfigured_ReturnsErrNotConfigured(t *testing.T) {
	c := keycloak.NewClient(keycloak.Config{}) // zero-value: ClientID empty
	ctx := context.Background()

	_, _, err := c.FindUserByEmail(ctx, "x@example.com")
	assert.ErrorIs(t, err, keycloak.ErrNotConfigured)

	_, _, err = c.GetUserByID(ctx, "some-id")
	assert.ErrorIs(t, err, keycloak.ErrNotConfigured)

	_, err = c.CreateUser(ctx, keycloak.CreateUserRequest{Email: "x@example.com"})
	assert.ErrorIs(t, err, keycloak.ErrNotConfigured)

	err = c.TriggerPasswordSetup(ctx, "some-id")
	assert.ErrorIs(t, err, keycloak.ErrNotConfigured)
}
