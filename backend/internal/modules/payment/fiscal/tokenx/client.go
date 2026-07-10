package tokenx

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
	// defaultTokenTTL is Token's documented access-token lifetime (24h). It is
	// used when the auth response omits expires_in.
	defaultTokenTTL = 86400 * time.Second
	// tokenRefreshSkew renews the token before it actually expires so an
	// in-flight request never races the expiry.
	tokenRefreshSkew = 5 * time.Minute
	// maxRetries bounds the 429 backoff (Token coding standard: 1s, 2s, 4s).
	maxRetries = 3
	// maxErrorBody caps how much of a failing response we keep in the error.
	maxErrorBody = 4 << 10
)

var retryBackoff = [maxRetries]time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// sleeper waits for d or until ctx is done. It is injected in tests so the
// backoff assertions do not burn seven real seconds.
type sleeper func(ctx context.Context, d time.Duration) error

func realSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Client is a Token X Connect Cloud REST client. It caches the 24h access
// token in memory, refreshes it lazily and collapses concurrent refreshes into
// a single request (singleflight). It is safe for concurrent use.
type Client struct {
	apiURL       string
	authURL      string
	clientID     string
	clientSecret string

	httpc *http.Client
	now   func() time.Time
	sleep sleeper

	sf singleflight.Group

	mu        sync.RWMutex
	token     string
	expiresAt time.Time
}

// ClientOption customizes a Client. Credentials and URLs come from Config; no
// adapter code reads the environment (fx injects everything).
type ClientOption func(*Client)

// WithHTTPClient replaces the underlying http.Client.
func WithHTTPClient(h *http.Client) ClientOption {
	return func(c *Client) { c.httpc = h }
}

// WithClock replaces the time source, letting tests expire the token.
func WithClock(now func() time.Time) ClientOption {
	return func(c *Client) { c.now = now }
}

// WithSleeper replaces the retry backoff wait.
func WithSleeper(s sleeper) ClientOption {
	return func(c *Client) { c.sleep = s }
}

// NewClient builds a Client from an already validated Config.
func NewClient(cfg Config, opts ...ClientOption) *Client {
	c := &Client{
		apiURL:       strings.TrimRight(cfg.APIURL, "/"),
		authURL:      strings.TrimRight(cfg.AuthURL, "/"),
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		httpc:        &http.Client{Timeout: 30 * time.Second},
		now:          func() time.Time { return time.Now().UTC() },
		sleep:        realSleep,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type authResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// accessToken returns a valid bearer token, fetching one if the cache is empty
// or close to expiry.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	if tok, ok := c.cachedToken(); ok {
		return tok, nil
	}
	// singleflight collapses a thundering herd of expiring-token requests into
	// one auth call; the double check inside covers the winner's followers.
	v, err, _ := c.sf.Do("token", func() (any, error) {
		if tok, ok := c.cachedToken(); ok {
			return tok, nil
		}
		return c.fetchToken(ctx)
	})
	if err != nil {
		return "", fmt.Errorf("tokenx: acquire access token: %w", err)
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

func (c *Client) fetchToken(ctx context.Context) (string, error) {
	endpoint := c.authURL + "/v1/auth/token"

	var out authResponse
	err := c.retry(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, http.NoBody)
		if err != nil {
			return fmt.Errorf("build auth request: %w", err)
		}
		req.SetBasicAuth(c.clientID, c.clientSecret)
		req.Header.Set("Accept", "application/json")
		return c.send(req, endpoint, &out)
	})
	if err != nil {
		return "", fmt.Errorf("tokenx auth: %w", err)
	}
	if out.AccessToken == "" {
		return "", errors.New("tokenx auth: response carries no access_token")
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

// invalidateToken drops the cached token so the next call re-authenticates.
func (c *Client) invalidateToken() {
	c.mu.Lock()
	c.token = ""
	c.expiresAt = time.Time{}
	c.mu.Unlock()
}

// retry runs do, backing off on 429 only. Any other 4xx is a permanent
// contract error, and retrying a basket POST blindly on 5xx risks duplicate
// device interaction, so both fail fast.
func (c *Client) retry(ctx context.Context, do func() error) error {
	var err error
	for attempt := 0; ; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		err = do()
		if err == nil {
			return nil
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || !apiErr.Retryable() || attempt >= maxRetries {
			return err
		}
		if sleepErr := c.sleep(ctx, retryBackoff[attempt]); sleepErr != nil {
			return fmt.Errorf("%w (last error: %v)", sleepErr, err)
		}
	}
}

// do performs an authenticated API call with the 429 retry policy.
func (c *Client) do(ctx context.Context, method, path string, headers map[string]string, body, out any) error {
	endpoint := c.apiURL + path

	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("marshal %s body: %w", path, err)
		}
	}

	return c.retry(ctx, func() error {
		tok, err := c.accessToken(ctx)
		if err != nil {
			return err
		}

		var reader io.Reader = http.NoBody
		if payload != nil {
			// A fresh reader per attempt: a retried request must resend the body.
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
		if err != nil {
			return fmt.Errorf("build %s request: %w", path, err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		if err := c.send(req, endpoint, out); err != nil {
			var apiErr *APIError
			// A rejected token is worthless: drop it so the next call re-auths.
			// We do not retry here — that is the caller's decision.
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
				c.invalidateToken()
			}
			return err
		}
		return nil
	})
}

// send executes req and decodes a 2xx JSON body into out (when out is non-nil).
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

// AddBasket posts a basket in list mode. The basket appears on every terminal
// of the branch; the cashier selects it on the device.
func (c *Client) AddBasket(ctx context.Context, vendorBranchRef string, b Basket) error {
	if vendorBranchRef == "" {
		return errors.New("tokenx: list mode requires a vendor branch ref")
	}
	return c.do(ctx, http.MethodPost, "/v1/basket", map[string]string{"branch-id": vendorBranchRef}, b, nil)
}

// AddInstantBasket posts a basket straight to one terminal's payment screen.
// The branch-id header must be absent here; sending it makes Token reject the
// request.
func (c *Client) AddInstantBasket(ctx context.Context, terminalSerial string, b Basket) error {
	if terminalSerial == "" {
		return errors.New("tokenx: instant mode requires a terminal serial")
	}
	return c.do(ctx, http.MethodPost, "/v1/instant-basket", map[string]string{"terminal-id": terminalSerial}, b, nil)
}

type fiscalInfoResponse struct {
	Result FiscalInfo `json:"result"`
}

// FiscalInfo fetches the terminal's sections (kısım) and tax rates. Callers
// persist them so buildBasket never guesses a sectionNo.
func (c *Client) FiscalInfo(ctx context.Context, terminalSerial string) (FiscalInfo, error) {
	var out fiscalInfoResponse
	path := "/v1/fiscal-info?terminal-id=" + url.QueryEscape(terminalSerial)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return FiscalInfo{}, err
	}
	return out.Result, nil
}

type openBasketsResponse struct {
	Result []OpenBasket `json:"result"`
}

// OpenBaskets lists baskets still awaiting payment on a terminal. Polling this
// endpoint on a hot path is forbidden (429); it exists for the reconciliation
// job that runs when a webhook is lost.
func (c *Client) OpenBaskets(ctx context.Context, terminalSerial string) ([]OpenBasket, error) {
	var out openBasketsResponse
	path := "/v1/baskets?terminal-id=" + url.QueryEscape(terminalSerial)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Result, nil
}

// DeleteBasket removes an open basket, e.g. an expired submission.
func (c *Client) DeleteBasket(ctx context.Context, basketID string) error {
	if basketID == "" {
		return errors.New("tokenx: basket id is required")
	}
	return c.do(ctx, http.MethodDelete, "/v1/baskets/"+url.PathEscape(basketID), nil, nil, nil)
}
