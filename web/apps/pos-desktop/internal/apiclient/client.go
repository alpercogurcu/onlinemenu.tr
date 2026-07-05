// Package apiclient is the single HTTP authority for the POS desktop app.
//
// Per lessons-from-b2b Bölüm 5 ("Tek token-refresh yolu"), the webview
// frontend never performs HTTP requests and never sees the session token.
// It calls Go methods through Wails bindings (see app.go); this Client is
// the only piece of code in the process that talks to the backend and the
// only piece of code that reads/writes the session token. There is
// structurally no second interceptor that could race a token refresh,
// because there is no HTTP client anywhere else in the binary.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// ErrUnauthenticated is returned by authenticated calls when no session
// token is available (Login was never called, or the session was cleared).
var ErrUnauthenticated = errors.New("apiclient: not authenticated")

// APIError wraps a non-2xx HTTP response from the backend.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("apiclient: unexpected status %d: %s", e.StatusCode, e.Body)
}

// Client is the sole HTTP + token authority of the POS desktop process.
//
// Wails invokes each bound method call on its own goroutine, so Client's
// methods are not implicitly serialized by the caller (e.g. a future
// connectivity-poll calling Ping concurrently with a user-triggered
// Login). tokenMu guards the in-memory token cache accordingly — see
// token/setToken/clearToken below. This is the only synchronization
// Client needs: baseURL and httpClient are immutable after New, and
// tokenstore.Store implementations are responsible for their own safety.
type Client struct {
	baseURL    string
	httpClient *http.Client
	tokens     tokenstore.Store

	tokenMu sync.RWMutex
	// currentToken caches the current session token in memory so
	// authenticated requests don't hit the token store (keychain I/O) on
	// every call. tokenstore remains the durable source of truth across
	// restarts.
	currentToken string
}

// New constructs a Client bound to baseURL, persisting sessions via store.
// The current token is eagerly loaded from store if one exists (e.g. the
// station was restarted with an active session).
func New(baseURL string, store tokenstore.Store) *Client {
	c := &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		tokens: store,
	}
	if tok, err := store.Load(); err == nil {
		c.setToken(tok)
	}
	return c
}

func (c *Client) token() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.currentToken
}

func (c *Client) setToken(token string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.currentToken = token
}

// IsAuthenticated reports whether a session token is currently held.
func (c *Client) IsAuthenticated() bool {
	return c.token() != ""
}

// Logout clears the in-memory and persisted session token.
func (c *Client) Logout() error {
	c.setToken("")
	if err := c.tokens.Clear(); err != nil {
		return fmt.Errorf("apiclient: logout: %w", err)
	}
	return nil
}

type loginRequest struct {
	Email string `json:"email"`
}

type loginResponse struct {
	Token    string `json:"token"`
	TenantID string `json:"tenant_id"`
	User     struct {
		ID       string `json:"id"`
		FullName string `json:"full_name"`
		Email    string `json:"email"`
	} `json:"user"`
}

// Session describes the authenticated identity returned by Login.
type Session struct {
	TenantID string
	UserID   string
	FullName string
	Email    string
}

// Login authenticates against the backend's dev-login endpoint
// (POST /dev/login, only registered when the backend runs with
// APP_ENV=dev) and persists the returned context token.
//
// The dev-login flow is a placeholder for the Keycloak-backed login the
// UI wave will implement; it exists so this skeleton has an end-to-end
// verifiable path from webview action to stored session token.
func (c *Client) Login(ctx context.Context, email string) (Session, error) {
	var resp loginResponse
	if err := c.do(ctx, http.MethodPost, "/dev/login", loginRequest{Email: email}, &resp); err != nil {
		return Session{}, fmt.Errorf("apiclient: login: %w", err)
	}

	if err := c.tokens.Save(resp.Token); err != nil {
		return Session{}, fmt.Errorf("apiclient: login: persist token: %w", err)
	}
	c.setToken(resp.Token)

	return Session{
		TenantID: resp.TenantID,
		UserID:   resp.User.ID,
		FullName: resp.User.FullName,
		Email:    resp.User.Email,
	}, nil
}

type whoAmIResponse struct {
	Person struct {
		ID       string `json:"id"`
		Email    string `json:"email"`
		FullName string `json:"full_name"`
		Phone    string `json:"phone"`
	} `json:"person"`
}

// WhoAmI calls GET /v1/identity/me to confirm the current session token is
// valid and to fetch the authenticated person's profile.
func (c *Client) WhoAmI(ctx context.Context) (Session, error) {
	if !c.IsAuthenticated() {
		return Session{}, ErrUnauthenticated
	}

	var resp whoAmIResponse
	if err := c.do(ctx, http.MethodGet, "/v1/identity/me", nil, &resp); err != nil {
		return Session{}, fmt.Errorf("apiclient: whoami: %w", err)
	}

	return Session{
		UserID:   resp.Person.ID,
		FullName: resp.Person.FullName,
		Email:    resp.Person.Email,
	}, nil
}

// Ping calls GET /healthz to verify backend reachability without requiring
// authentication. Useful for a connectivity indicator in the UI.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("apiclient: ping: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("apiclient: ping: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return nil
}

// do performs an HTTP request against the backend, attaching the current
// session token (if any) and decoding a JSON response into out.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var bodyReader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok := c.token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
