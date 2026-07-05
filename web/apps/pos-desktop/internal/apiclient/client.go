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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// ErrUnauthenticated is returned by authenticated calls when no session
// token is available (Login was never called, or the session was cleared).
var ErrUnauthenticated = errors.New("apiclient: not authenticated")

// idempotencyHeader is the header name the backend's ADR-SEC-003 middleware
// (httpx.Idempotency) requires on POST /orders, POST /checks/{id}/close and
// POST /payments.
const idempotencyHeader = "Idempotency-Key"

const (
	// maxIdempotentAttempts bounds retries of an idempotency-key-gated
	// request. Every attempt reuses the same key and body (see
	// doIdempotent), so retrying is safe: the backend either replays the
	// cached response or executes the write exactly once.
	maxIdempotentAttempts = 3
	idempotentRetryBase   = 200 * time.Millisecond
)

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

// Session describes the authenticated identity returned by Login/WhoAmI.
// BranchID is empty for a chain-wide staff session (see Client.claims) —
// callers that need a branch to open a check must check for that case.
type Session struct {
	TenantID string
	BranchID string
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

	session := Session{
		TenantID: resp.TenantID,
		UserID:   resp.User.ID,
		FullName: resp.User.FullName,
		Email:    resp.User.Email,
	}
	if _, branchID, ok := c.claims(); ok && branchID != uuid.Nil {
		session.BranchID = branchID.String()
	}
	return session, nil
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

	session := Session{
		UserID:   resp.Person.ID,
		FullName: resp.Person.FullName,
		Email:    resp.Person.Email,
	}
	// GET /v1/identity/me returns only person profile fields — tenant/branch
	// are not part of that response (verified against me_handler.go). They
	// only exist inside this client's own CTX token, so decode them from
	// there instead of leaving a restored session without a branch.
	if tenantID, branchID, ok := c.claims(); ok {
		session.TenantID = tenantID.String()
		if branchID != uuid.Nil {
			session.BranchID = branchID.String()
		}
	}
	return session, nil
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
	return c.doWithHeaders(ctx, method, path, body, out, nil)
}

// doWithHeaders is do plus caller-supplied extra headers (currently only
// used to attach Idempotency-Key — see doIdempotent).
func (c *Client) doWithHeaders(ctx context.Context, method, path string, body, out any, headers map[string]string) error {
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
	for k, v := range headers {
		req.Header.Set(k, v)
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

// doIdempotent performs a POST that the backend gates on ADR-SEC-003
// (PlaceOrder, CloseCheck, RegisterCashPayment). It mints exactly ONE
// Idempotency-Key for the whole logical operation — not one per HTTP
// attempt — and resends the identical key and body on every retry.
//
// This matters because the key identifies one logical request to the
// server, not a request-attempt slot: minting a new key per retry would let
// a client that timed out waiting for a response (but whose write actually
// committed) double-execute the write on retry, which is exactly the
// failure ADR-SEC-003 exists to prevent.
//
// Retries only happen for outcomes that mean "we don't yet know whether the
// server durably saw this write" — a transport-level error (no HTTP
// response at all) or a 5xx (server-side failure, safe to replay under the
// same key/body pair). Any 4xx response (400/401/403/404/409/422) means the
// server made a decision the client cannot change by retrying — including
// 409 "Idempotency-Key is already being processed" and 422 "already used
// with a different body" — so those are returned immediately, unretried.
func (c *Client) doIdempotent(ctx context.Context, method, path string, body, out any) error {
	key := uuid.NewString()
	headers := map[string]string{idempotencyHeader: key}

	var lastErr error
	for attempt := 0; attempt < maxIdempotentAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(idempotentRetryBase * time.Duration(1<<uint(attempt-1))):
			}
		}

		err := c.doWithHeaders(ctx, method, path, body, out, headers)
		if err == nil {
			return nil
		}
		lastErr = err

		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode < 500 {
			return err
		}
		// Transport error or 5xx: safe to retry with the same key/body.
	}
	return lastErr
}

// sessionClaims mirrors the subset of the backend's context-token payload
// (auth.contextTokenClaims — tid/bid) this client needs to surface branch
// info to the UI. Login/WhoAmI HTTP responses do not carry tenant/branch
// (verified against backend/internal/modules/identity/http/me_handler.go
// and cmd/api/main.go's devLoginResp) — the only place that information
// exists client-side is inside the CTX token itself.
type sessionClaims struct {
	TenantID string `json:"tid"`
	BranchID string `json:"bid"`
}

// claims decodes the tenant/branch pair embedded in the current session
// token's payload segment, WITHOUT verifying its signature.
//
// This is safe despite skipping verification: Client is the only code in
// the process that ever sets currentToken, and it only ever does so from
// (a) this client's own successful Login call, or (b) tokenstore restoring
// the exact bytes this same client previously wrote — never from an
// external or untrusted source. It is also not a security boundary: every
// subsequent request still carries this token as a Bearer credential and is
// independently re-verified (signature + expiry) by the backend on every
// call. This decode exists purely to let the UI display/prefill the
// station's branch — it must never be used as an authorization decision.
func (c *Client) claims() (tenantID, branchID uuid.UUID, ok bool) {
	tok := c.token()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return uuid.Nil, uuid.Nil, false
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	var claims sessionClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	tid, err := uuid.Parse(claims.TenantID)
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	// BranchID is empty for a chain-wide staff session (dev-login's "first
	// active membership" can be branch-less) — that is a valid, meaningful
	// zero value, not a decode failure.
	bid, _ := uuid.Parse(claims.BranchID)
	return tid, bid, true
}

// CurrentBranchID returns the branch encoded in the current session token,
// or "" if there is no session, the token can't be decoded, or the session
// is chain-wide (no single branch). See claims for the trust rationale.
func (c *Client) CurrentBranchID() string {
	_, branchID, ok := c.claims()
	if !ok || branchID == uuid.Nil {
		return ""
	}
	return branchID.String()
}
