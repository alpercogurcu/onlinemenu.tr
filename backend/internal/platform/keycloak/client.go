package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	// defaultTokenTTL is used when the token response omits expires_in.
	defaultTokenTTL = 60 * time.Second
	// tokenRefreshSkew renews the token before it actually expires so an
	// in-flight request never races the expiry.
	tokenRefreshSkew = 10 * time.Second
	// requestTimeout bounds every Admin API HTTP call.
	requestTimeout = 15 * time.Second
	// maxErrorBody caps how much of a failing response body is kept in errors.
	maxErrorBody = 4 << 10
)

// AdminAPI is the identity module's boundary onto Keycloak's Admin API. It is
// satisfied by *Client; tests inject a fake so no live Keycloak instance is
// required to exercise the staff invite flow.
type AdminAPI interface {
	// FindUserByEmail looks up a user by exact email match. found is false
	// (with a nil error) when no such user exists.
	FindUserByEmail(ctx context.Context, email string) (user User, found bool, err error)
	// CreateUser creates a new realm user. Returns ErrUserAlreadyExists on a
	// 409 — the caller must recover by calling FindUserByEmail again.
	CreateUser(ctx context.Context, req CreateUserRequest) (User, error)
	// TriggerPasswordSetup asks Keycloak to email the user a password-setup
	// (execute-actions) link. A non-nil error means the email did not go
	// out (e.g. the realm has no SMTP configured) — callers must surface
	// this to the invoker, never swallow it (docs/lessons-from-b2b.md).
	TriggerPasswordSetup(ctx context.Context, userID string) error
}

// Client is a Keycloak Admin REST API client. It caches the client_credentials
// access token in memory, refreshes it lazily and collapses concurrent
// refreshes into a single request (singleflight), mirroring
// payment/fiscal/tokenx.Client. It is safe for concurrent use.
type Client struct {
	cfg Config

	tokenURL      string
	adminUsersURL string

	httpc *http.Client
	now   func() time.Time

	sf singleflight.Group

	mu        sync.RWMutex
	token     string
	expiresAt time.Time
}

var _ AdminAPI = (*Client)(nil)

// ClientOption customizes a Client.
type ClientOption func(*Client)

// WithHTTPClient replaces the underlying http.Client. Used by tests.
func WithHTTPClient(h *http.Client) ClientOption {
	return func(c *Client) { c.httpc = h }
}

// WithClock replaces the time source. Used by tests to expire the token.
func WithClock(now func() time.Time) ClientOption {
	return func(c *Client) { c.now = now }
}

// NewClient builds a Client from cfg. An unconfigured Config (see
// Config.enabled) is accepted, not rejected: every method call on the
// resulting Client returns ErrNotConfigured instead of the process failing to
// boot. This mirrors payment.newFiscalAdapter's mock fallback and keeps
// cmd/api bootable in dev/CI without a Keycloak admin service account.
func NewClient(cfg Config, opts ...ClientOption) *Client {
	base := strings.TrimRight(cfg.BaseURL, "/")
	c := &Client{
		cfg:           cfg,
		tokenURL:      base + "/realms/" + cfg.Realm + "/protocol/openid-connect/token",
		adminUsersURL: base + "/admin/realms/" + cfg.Realm + "/users",
		httpc:         &http.Client{Timeout: requestTimeout},
		now:           func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// APIError reports a non-2xx response from a Keycloak Admin API call.
type APIError struct {
	StatusCode int
	Endpoint   string
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("keycloak: %s returned %d: %s", e.Endpoint, e.StatusCode, e.Body)
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// accessToken returns a valid bearer token, fetching one if the cache is
// empty or close to expiry.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	if tok, ok := c.cachedToken(); ok {
		return tok, nil
	}
	v, err, _ := c.sf.Do("token", func() (any, error) {
		if tok, ok := c.cachedToken(); ok {
			return tok, nil
		}
		return c.fetchToken(ctx)
	})
	if err != nil {
		return "", fmt.Errorf("keycloak: acquire access token: %w", err)
	}
	return v.(string), nil
}

func (c *Client) cachedToken() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.token == "" {
		return "", false
	}
	if !c.now().Before(c.expiresAt.Add(-tokenRefreshSkew)) {
		return "", false
	}
	return c.token, true
}

// fetchToken performs the client_credentials grant.
func (c *Client) fetchToken(ctx context.Context) (string, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("keycloak auth: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	var out tokenResponse
	if err := c.send(req, c.tokenURL, &out); err != nil {
		return "", fmt.Errorf("keycloak auth: %w", err)
	}
	if out.AccessToken == "" {
		return "", errors.New("keycloak auth: response carries no access_token")
	}

	ttl := defaultTokenTTL
	if out.ExpiresIn > 0 {
		ttl = time.Duration(out.ExpiresIn) * time.Second
	}

	c.mu.Lock()
	c.token = out.AccessToken
	c.expiresAt = c.now().Add(ttl)
	c.mu.Unlock()

	return out.AccessToken, nil
}

func (c *Client) invalidateToken() {
	c.mu.Lock()
	c.token = ""
	c.expiresAt = time.Time{}
	c.mu.Unlock()
}

// do performs an authenticated Admin API call. On 401 the cached token is
// dropped so the next call re-authenticates; it does not retry itself.
func (c *Client) do(ctx context.Context, method, endpoint string, body any, out any) (*http.Response, error) {
	tok, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("keycloak: marshal %s body: %w", endpoint, err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("keycloak: build %s request: %w", endpoint, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("keycloak: call %s: %w", endpoint, err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		c.invalidateToken()
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return resp, &APIError{StatusCode: resp.StatusCode, Endpoint: endpoint, Body: string(respBody)}
	}

	if out != nil {
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp, fmt.Errorf("keycloak: decode %s response: %w", endpoint, err)
		}
		return resp, nil
	}

	// No caller-supplied decode target (e.g. CreateUser only needs the
	// Location header): drain and close the body so the connection is
	// returned to the pool instead of leaking.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp, nil
}

// send is a do() wrapper used by fetchToken, where the response body must
// always be drained/closed and no *http.Response is needed by the caller.
func (c *Client) send(req *http.Request, endpoint string, out any) error {
	resp, err := c.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", endpoint, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return &APIError{StatusCode: resp.StatusCode, Endpoint: endpoint, Body: string(body)}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", endpoint, err)
	}
	return nil
}

// FindUserByEmail implements AdminAPI.
func (c *Client) FindUserByEmail(ctx context.Context, email string) (User, bool, error) {
	if !c.cfg.enabled() {
		return User{}, false, ErrNotConfigured
	}

	endpoint := c.adminUsersURL + "?email=" + url.QueryEscape(email) + "&exact=true"
	var out []keycloakUserRepresentation
	if _, err := c.do(ctx, http.MethodGet, endpoint, nil, &out); err != nil {
		return User{}, false, fmt.Errorf("keycloak: find user by email: %w", err)
	}
	if len(out) == 0 {
		return User{}, false, nil
	}
	return toUser(out[0]), true, nil
}

// CreateUser implements AdminAPI.
func (c *Client) CreateUser(ctx context.Context, req CreateUserRequest) (User, error) {
	if !c.cfg.enabled() {
		return User{}, ErrNotConfigured
	}

	body := keycloakUserRepresentation{
		Username:      req.Email,
		Email:         req.Email,
		FirstName:     req.FullName,
		Enabled:       true,
		EmailVerified: false,
	}

	resp, err := c.do(ctx, http.MethodPost, c.adminUsersURL, body, nil)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
			return User{}, ErrUserAlreadyExists
		}
		return User{}, fmt.Errorf("keycloak: create user: %w", err)
	}

	id, err := userIDFromLocation(resp.Header.Get("Location"))
	if err != nil {
		return User{}, fmt.Errorf("keycloak: create user: %w", err)
	}
	return User{ID: id, Username: req.Email, Email: req.Email, Enabled: true}, nil
}

// TriggerPasswordSetup implements AdminAPI.
func (c *Client) TriggerPasswordSetup(ctx context.Context, userID string) error {
	if !c.cfg.enabled() {
		return ErrNotConfigured
	}

	endpoint := c.adminUsersURL + "/" + url.PathEscape(userID) + "/execute-actions-email"
	actions := []string{"UPDATE_PASSWORD"}
	if _, err := c.do(ctx, http.MethodPut, endpoint, actions, nil); err != nil {
		return fmt.Errorf("keycloak: trigger password setup email: %w", err)
	}
	return nil
}

// userIDFromLocation extracts the trailing path segment (the new user's id)
// from the Location header Keycloak returns on a successful user creation,
// e.g. ".../admin/realms/onlinemenu/users/3f9c...".
func userIDFromLocation(location string) (string, error) {
	if location == "" {
		return "", errors.New("response carries no Location header")
	}
	parts := strings.Split(strings.TrimRight(location, "/"), "/")
	id := parts[len(parts)-1]
	if id == "" {
		return "", fmt.Errorf("could not parse user id from Location header %q", location)
	}
	return id, nil
}
