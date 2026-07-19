package tokenx

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
}

// serverOpts configures newTestServer before the server starts, so no handler
// state is mutated while requests are in flight.
type serverOpts struct {
	api       http.HandlerFunc
	auth      http.HandlerFunc
	authDelay time.Duration
}

type testServer struct {
	*httptest.Server
	authHits   atomic.Int64
	authHeader atomic.Pointer[string]

	mu       sync.Mutex
	requests []recordedRequest
}

func (s *testServer) recorded() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

func (s *testServer) lastRequest(t *testing.T) recordedRequest {
	t.Helper()
	reqs := s.recorded()
	require.NotEmpty(t, reqs, "no API request recorded")
	return reqs[len(reqs)-1]
}

// newTestServer serves the auth endpoint itself and delegates every API path to
// opts.api (nil means "200 with an empty JSON object").
func newTestServer(t *testing.T, opts serverOpts) *testServer {
	t.Helper()
	ts := &testServer{}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/token", func(w http.ResponseWriter, r *http.Request) {
		n := ts.authHits.Add(1)
		header := r.Header.Get("Authorization")
		ts.authHeader.Store(&header)
		if opts.authDelay > 0 {
			time.Sleep(opts.authDelay)
		}
		if opts.auth != nil {
			opts.auth(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": fmt.Sprintf("tok-%d", n),
			"expires_in":   86400,
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts.mu.Lock()
		ts.requests = append(ts.requests, recordedRequest{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
			Header: r.Header.Clone(), Body: body,
		})
		ts.mu.Unlock()

		if opts.api != nil {
			opts.api(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	ts.Server = httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type sleepRecorder struct {
	mu    sync.Mutex
	calls []time.Duration
}

func (s *sleepRecorder) sleep(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, d)
	return nil
}

func (s *sleepRecorder) recorded() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.calls...)
}

func testConfig(srv *testServer) Config {
	return Config{
		APIURL:       srv.URL,
		AuthURL:      srv.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		BasketMode:   BasketModeInstant,
	}
}

func newTestClient(srv *testServer, opts ...ClientOption) (*Client, *fakeClock, *sleepRecorder) {
	clock := &fakeClock{t: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)}
	sleeps := &sleepRecorder{}
	base := []ClientOption{
		WithHTTPClient(srv.Client()),
		WithClock(clock.now),
		WithSleeper(sleeps.sleep),
	}
	return NewClient(testConfig(srv), append(base, opts...)...), clock, sleeps
}

func TestClientAuthUsesBasicAuthAndBearerToken(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{})
	client, _, _ := newTestClient(srv)

	require.NoError(t, client.AddInstantBasket(context.Background(), "AV0000000658", Basket{}))

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("client-id:client-secret"))
	got := srv.authHeader.Load()
	require.NotNil(t, got)
	assert.Equal(t, want, *got, "auth endpoint must use Basic base64(client_id:client_secret)")
	assert.Equal(t, "Bearer tok-1", srv.lastRequest(t).Header.Get("Authorization"))
}

func TestClientCachesTokenAcrossCalls(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{})
	client, _, _ := newTestClient(srv)

	for range 3 {
		require.NoError(t, client.AddInstantBasket(context.Background(), "T1", Basket{}))
	}
	assert.Equal(t, int64(1), srv.authHits.Load(), "token must be fetched once and cached")
}

func TestClientRefreshesExpiredToken(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{})
	client, clock, _ := newTestClient(srv)

	require.NoError(t, client.AddInstantBasket(context.Background(), "T1", Basket{}))
	require.Equal(t, int64(1), srv.authHits.Load())

	// Still inside the validity window minus the refresh skew.
	clock.advance(defaultTokenTTL - tokenRefreshSkew - time.Minute)
	require.NoError(t, client.AddInstantBasket(context.Background(), "T1", Basket{}))
	assert.Equal(t, int64(1), srv.authHits.Load(), "token still valid, must not refresh")
	assert.Equal(t, "Bearer tok-1", srv.lastRequest(t).Header.Get("Authorization"))

	// Cross the skew boundary: the token must be renewed before it expires.
	clock.advance(2 * time.Minute)
	require.NoError(t, client.AddInstantBasket(context.Background(), "T1", Basket{}))
	assert.Equal(t, int64(2), srv.authHits.Load(), "token near expiry must be refreshed")
	assert.Equal(t, "Bearer tok-2", srv.lastRequest(t).Header.Get("Authorization"))
}

func TestClientDeduplicatesConcurrentTokenFetches(t *testing.T) {
	t.Parallel()
	// Hold the auth response open so every goroutine piles onto the same flight.
	srv := newTestServer(t, serverOpts{authDelay: 50 * time.Millisecond})
	client, _, _ := newTestClient(srv)

	const goroutines = 50
	var wg sync.WaitGroup
	start := make(chan struct{})
	tokens := make([]string, goroutines)
	errs := make([]error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tokens[i], errs[i] = client.accessToken(context.Background())
		}()
	}
	close(start)
	wg.Wait()

	for i := range goroutines {
		require.NoError(t, errs[i])
		assert.Equal(t, "tok-1", tokens[i], "all callers must share one token")
	}
	assert.Equal(t, int64(1), srv.authHits.Load(), "concurrent refreshes must collapse into one auth call")
}

func TestClientRetriesOnRateLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		statuses     []int
		wantSleeps   []time.Duration
		wantRequests int
		wantErr      bool
		wantStatus   int
	}{
		{
			name:         "succeeds after two 429s",
			statuses:     []int{429, 429, 200},
			wantSleeps:   []time.Duration{time.Second, 2 * time.Second},
			wantRequests: 3,
		},
		{
			name:         "gives up after three retries",
			statuses:     []int{429, 429, 429, 429},
			wantSleeps:   []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
			wantRequests: 4,
			wantErr:      true,
			wantStatus:   429,
		},
		{
			name:         "does not retry a 400",
			statuses:     []int{400},
			wantSleeps:   nil,
			wantRequests: 1,
			wantErr:      true,
			wantStatus:   400,
		},
		{
			name:         "does not retry a 404",
			statuses:     []int{404},
			wantSleeps:   nil,
			wantRequests: 1,
			wantErr:      true,
			wantStatus:   404,
		},
		{
			name:         "does not retry a 500",
			statuses:     []int{500},
			wantSleeps:   nil,
			wantRequests: 1,
			wantErr:      true,
			wantStatus:   500,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int64
			srv := newTestServer(t, serverOpts{api: func(w http.ResponseWriter, _ *http.Request) {
				i := int(calls.Add(1)) - 1
				if i >= len(tc.statuses) {
					i = len(tc.statuses) - 1
				}
				w.WriteHeader(tc.statuses[i])
				_, _ = w.Write([]byte(`{"message":"rate limited"}`))
			}})
			client, _, sleeps := newTestClient(srv)

			err := client.AddInstantBasket(context.Background(), "T1", Basket{})
			if tc.wantErr {
				require.Error(t, err)
				var apiErr *APIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, tc.wantStatus, apiErr.StatusCode)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantSleeps, sleeps.recorded(), "backoff schedule must be 1s/2s/4s")
			assert.Equal(t, tc.wantRequests, len(srv.recorded()))
		})
	}
}

// TestClientDoesNotNestAuthRetries pins the fix for a nested-retry bug: before
// the fix, a persistently rate-limited auth endpoint was retried inside
// fetchToken (up to maxRetries+1 attempts) AND inside do()'s outer retry
// (another maxRetries+1 iterations that each re-triggered fetchToken),
// compounding into (maxRetries+1)^2 auth calls. With a single retry layer,
// the auth endpoint may be hit at most maxRetries+1 times total, using the
// same shared backoff schedule, and the basket endpoint is never reached
// because auth never succeeds.
func TestClientDoesNotNestAuthRetries(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{
		auth: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) },
		api:  func(http.ResponseWriter, *http.Request) { t.Error("API must not be called when auth never succeeds") },
	})
	client, _, sleeps := newTestClient(srv)

	err := client.AddInstantBasket(context.Background(), "T1", Basket{})
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)

	assert.Equal(t, int64(maxRetries+1), srv.authHits.Load(),
		"auth must be retried through the single shared budget, not nested (maxRetries+1, not (maxRetries+1)^2)")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, sleeps.recorded(),
		"backoff schedule must be the single shared 1s/2s/4s budget")
}

func TestClientRetryResendsRequestBody(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	srv := newTestServer(t, serverOpts{api: func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}})
	client, _, _ := newTestClient(srv)

	basket := Basket{BasketID: "abc", Items: []BasketItem{{Name: "Çay", Price: 1500}}}
	require.NoError(t, client.AddInstantBasket(context.Background(), "T1", basket))

	reqs := srv.recorded()
	require.Len(t, reqs, 2)
	assert.JSONEq(t, string(reqs[0].Body), string(reqs[1].Body), "retried request must resend the body")
	assert.NotEmpty(t, reqs[1].Body)
}

func TestClientInvalidatesTokenOnUnauthorized(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	srv := newTestServer(t, serverOpts{api: func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}})
	client, _, _ := newTestClient(srv)

	require.Error(t, client.AddInstantBasket(context.Background(), "T1", Basket{}))
	require.Equal(t, int64(1), srv.authHits.Load())

	// The rejected token was dropped, so the next call re-authenticates.
	require.NoError(t, client.AddInstantBasket(context.Background(), "T1", Basket{}))
	assert.Equal(t, int64(2), srv.authHits.Load())
	assert.Equal(t, "Bearer tok-2", srv.lastRequest(t).Header.Get("Authorization"))
}

func TestClientAuthFailureIsReported(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{
		auth: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
		api:  func(http.ResponseWriter, *http.Request) { t.Error("API must not be called when auth fails") },
	})
	client, _, _ := newTestClient(srv)

	err := client.AddInstantBasket(context.Background(), "T1", Basket{})
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
}

func TestClientAuthRejectsEmptyToken(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{
		auth: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"access_token":""}`)) },
		api:  func(http.ResponseWriter, *http.Request) { t.Error("API must not be called without a token") },
	})
	client, _, _ := newTestClient(srv)

	err := client.AddInstantBasket(context.Background(), "T1", Basket{})
	require.ErrorContains(t, err, "no access_token")
}

func TestClientBasketEndpointsUseTheRightHeaders(t *testing.T) {
	t.Parallel()

	t.Run("instant basket sends terminal-id and never branch-id", func(t *testing.T) {
		t.Parallel()
		srv := newTestServer(t, serverOpts{})
		client, _, _ := newTestClient(srv)

		require.NoError(t, client.AddInstantBasket(context.Background(), "AV0000000658", Basket{BasketID: "b1"}))

		req := srv.lastRequest(t)
		assert.Equal(t, http.MethodPost, req.Method)
		assert.Equal(t, "/v1/instant-basket", req.Path)
		assert.Equal(t, "AV0000000658", req.Header.Get("terminal-id"))
		assert.Empty(t, req.Header.Get("branch-id"), "branch-id on instant basket makes Token reject the request")
	})

	t.Run("list basket sends branch-id and never terminal-id", func(t *testing.T) {
		t.Parallel()
		srv := newTestServer(t, serverOpts{})
		client, _, _ := newTestClient(srv)

		require.NoError(t, client.AddBasket(context.Background(), "BR-42", Basket{BasketID: "b1"}))

		req := srv.lastRequest(t)
		assert.Equal(t, http.MethodPost, req.Method)
		assert.Equal(t, "/v1/basket", req.Path)
		assert.Equal(t, "BR-42", req.Header.Get("branch-id"))
		assert.Empty(t, req.Header.Get("terminal-id"))
	})

	t.Run("missing routing identifiers are rejected before the call", func(t *testing.T) {
		t.Parallel()
		srv := newTestServer(t, serverOpts{})
		client, _, _ := newTestClient(srv)

		require.Error(t, client.AddInstantBasket(context.Background(), "", Basket{}))
		require.Error(t, client.AddBasket(context.Background(), "", Basket{}))
		require.Error(t, client.DeleteBasket(context.Background(), ""))
		assert.Empty(t, srv.recorded(), "no request must leave without a routing identifier")
	})
}

func TestClientFiscalInfo(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{api: func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"sections":[
			{"sectionNo":1,"name":"Yiyecek","taxPercent":1000,"type":0,"limit":100000,"price":0},
			{"sectionNo":2,"name":"İçecek","taxPercent":2000,"type":0,"limit":0,"price":0}
		],"terminal":{"serialNumber":"AV0000000658"}}}`))
	}})
	client, _, _ := newTestClient(srv)

	info, err := client.FiscalInfo(context.Background(), "AV0000000658")
	require.NoError(t, err)

	req := srv.lastRequest(t)
	assert.Equal(t, "/v1/fiscal-info", req.Path)
	assert.Equal(t, "terminal-id=AV0000000658", req.Query)

	require.Len(t, info.Sections, 2)
	assert.Equal(t, Section{SectionNo: 1, Name: "Yiyecek", TaxPercent: 1000, Limit: 100000}, info.Sections[0])
	assert.Equal(t, 2000, info.Sections[1].TaxPercent)
	assert.JSONEq(t, `{"serialNumber":"AV0000000658"}`, string(info.Terminal))
}

func TestClientOpenBaskets(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{api: func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":[{"basketID":"b-1","title":"Masa 5","checkNumber":42}]}`))
	}})
	client, _, _ := newTestClient(srv)

	baskets, err := client.OpenBaskets(context.Background(), "T1")
	require.NoError(t, err)
	require.Len(t, baskets, 1)
	assert.Equal(t, OpenBasket{BasketID: "b-1", Title: "Masa 5", CheckNumber: 42}, baskets[0])
	assert.Equal(t, "terminal-id=T1", srv.lastRequest(t).Query)
}

func TestClientDeleteBasket(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{})
	client, _, _ := newTestClient(srv)

	require.NoError(t, client.DeleteBasket(context.Background(), "6ba7b810-9dad-11d1-80b4-00c04fd430c8"))
	req := srv.lastRequest(t)
	assert.Equal(t, http.MethodDelete, req.Method)
	assert.Equal(t, "/v1/baskets/6ba7b810-9dad-11d1-80b4-00c04fd430c8", req.Path)
}

func TestClientHonoursContextCancellation(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{})
	client, _, _ := newTestClient(srv)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.AddInstantBasket(ctx, "T1", Basket{})
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, srv.recorded())
}
