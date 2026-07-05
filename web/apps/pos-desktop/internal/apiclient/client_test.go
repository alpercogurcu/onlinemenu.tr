package apiclient

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// memStore is an in-memory tokenstore.Store double for tests, keeping
// apiclient tests independent of the OS keychain / filesystem. It is
// mutex-protected so it stays race-detector-clean when exercised
// concurrently (see TestClient_ConcurrentLoginAndPing_NoDataRace).
type memStore struct {
	mu    sync.Mutex
	token string
	saved bool
}

func (m *memStore) Save(token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = token
	m.saved = true
	return nil
}

func (m *memStore) Load() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.saved {
		return "", tokenstore.ErrNoToken
	}
	return m.token, nil
}

func (m *memStore) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = ""
	m.saved = false
	return nil
}

func TestClient_Login_PersistsTokenAndReturnsSession(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dev/login" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		gotAuthHeader = r.Header.Get("Authorization")

		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Email != "cashier@example.com" {
			t.Fatalf("email = %q, want cashier@example.com", req.Email)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(loginResponse{
			Token:    "ctx-token-xyz",
			TenantID: "tenant-1",
			User: struct {
				ID       string `json:"id"`
				FullName string `json:"full_name"`
				Email    string `json:"email"`
			}{ID: "person-1", FullName: "Cashier One", Email: "cashier@example.com"},
		})
	}))
	defer srv.Close()

	store := &memStore{}
	c := New(srv.URL, store)

	session, err := c.Login(t.Context(), "cashier@example.com")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if gotAuthHeader != "" {
		t.Fatalf("login request should not carry an Authorization header, got %q", gotAuthHeader)
	}
	if session.TenantID != "tenant-1" || session.UserID != "person-1" {
		t.Fatalf("unexpected session: %+v", session)
	}
	if !store.saved || store.token != "ctx-token-xyz" {
		t.Fatalf("token was not persisted to store: %+v", store)
	}
	if !c.IsAuthenticated() {
		t.Fatal("client should be authenticated after Login")
	}
}

func TestClient_WhoAmI_RequiresAuthentication(t *testing.T) {
	c := New("http://unused.invalid", &memStore{})

	_, err := c.WhoAmI(t.Context())
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("got %v, want ErrUnauthenticated", err)
	}
}

func TestClient_WhoAmI_SendsBearerTokenAndParsesProfile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/identity/me" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer ctx-token-xyz" {
			t.Fatalf("Authorization header = %q, want Bearer ctx-token-xyz", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whoAmIResponse{
			Person: struct {
				ID       string `json:"id"`
				Email    string `json:"email"`
				FullName string `json:"full_name"`
				Phone    string `json:"phone"`
			}{ID: "person-1", Email: "cashier@example.com", FullName: "Cashier One"},
		})
	}))
	defer srv.Close()

	store := &memStore{token: "ctx-token-xyz", saved: true}
	c := New(srv.URL, store)

	session, err := c.WhoAmI(t.Context())
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if session.UserID != "person-1" || session.Email != "cashier@example.com" {
		t.Fatalf("unexpected session: %+v", session)
	}
}

func TestClient_Ping_ReturnsAPIErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("db down"))
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{})

	err := c.Ping(t.Context())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("StatusCode = %d, want 503", apiErr.StatusCode)
	}
}

func TestClient_Logout_ClearsTokenFromStoreAndMemory(t *testing.T) {
	store := &memStore{token: "ctx-token-xyz", saved: true}
	c := New("http://unused.invalid", store)

	if err := c.Logout(); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if c.IsAuthenticated() {
		t.Fatal("client should not be authenticated after Logout")
	}
	if store.saved {
		t.Fatal("store should have cleared token")
	}
}

func TestClient_New_RestoresSessionFromStore(t *testing.T) {
	store := &memStore{token: "restored-token", saved: true}
	c := New("http://unused.invalid", store)

	if !c.IsAuthenticated() {
		t.Fatal("client should restore authentication state from store on New")
	}
}

// TestClient_ConcurrentLoginAndPing_NoDataRace guards against a regression
// where a future connectivity-poll (Ping) fires concurrently with a
// user-triggered Login/Logout — Wails invokes each bound method call on
// its own goroutine, so Client cannot assume its methods are called
// serially. Run with `go test -race` (see task pos:test).
func TestClient_ConcurrentLoginAndPing_NoDataRace(t *testing.T) {
	var loginCount atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/dev/login":
			loginCount.Add(1)
			_ = json.NewEncoder(w).Encode(loginResponse{Token: "concurrent-token"})
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{})

	const workers = 20
	var wg sync.WaitGroup
	wg.Add(workers * 3)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			_, _ = c.Login(t.Context(), "cashier@example.com")
		}()
		go func() {
			defer wg.Done()
			_ = c.Ping(t.Context())
		}()
		go func() {
			defer wg.Done()
			_ = c.IsAuthenticated()
		}()
	}
	wg.Wait()

	if loginCount.Load() != workers {
		t.Fatalf("login requests reached backend = %d, want %d", loginCount.Load(), workers)
	}
}
