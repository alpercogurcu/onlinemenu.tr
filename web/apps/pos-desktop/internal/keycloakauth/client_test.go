package keycloakauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New(Config{BaseURL: srv.URL, Realm: "onlinemenu", ClientID: "pos-desktop"})
	return c, srv
}

func TestClient_AuthorizeURL_ContainsPKCEAndRedirect(t *testing.T) {
	c := New(Config{BaseURL: "http://localhost:8090", Realm: "onlinemenu", ClientID: "pos-desktop"})

	authURL := c.AuthorizeURL(AuthorizeURLParams{
		RedirectURI:   "http://127.0.0.1:54321/callback",
		State:         "state-1",
		Nonce:         "nonce-1",
		CodeChallenge: "challenge-1",
	})

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	if !strings.HasPrefix(authURL, "http://localhost:8090/realms/onlinemenu/protocol/openid-connect/auth?") {
		t.Fatalf("authURL = %q, unexpected base", authURL)
	}
	q := parsed.Query()
	want := map[string]string{
		"client_id":             "pos-desktop",
		"response_type":         "code",
		"scope":                 "openid",
		"redirect_uri":          "http://127.0.0.1:54321/callback",
		"state":                 "state-1",
		"nonce":                 "nonce-1",
		"code_challenge":        "challenge-1",
		"code_challenge_method": "S256",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Fatalf("query param %q = %q, want %q", k, got, v)
		}
	}
}

func TestClient_EndSessionURL_WithAndWithoutIDTokenHint(t *testing.T) {
	c := New(Config{BaseURL: "http://localhost:8090", Realm: "onlinemenu", ClientID: "pos-desktop"})

	withHint := c.EndSessionURL("id-token-abc")
	if !strings.Contains(withHint, "id_token_hint=id-token-abc") {
		t.Fatalf("EndSessionURL(with hint) = %q, missing id_token_hint", withHint)
	}

	withoutHint := c.EndSessionURL("")
	if strings.Contains(withoutHint, "id_token_hint") {
		t.Fatalf("EndSessionURL(\"\") = %q, should not contain id_token_hint", withoutHint)
	}
	if !strings.Contains(withoutHint, "client_id=pos-desktop") {
		t.Fatalf("EndSessionURL(\"\") = %q, missing client_id", withoutHint)
	}
}

func TestClient_Exchange_SendsAuthorizationCodeGrantWithPKCEVerifier(t *testing.T) {
	var gotBody url.Values
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/realms/onlinemenu/protocol/openid-connect/token" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotBody = r.Form

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-1","refresh_token":"rt-1","id_token":"idt-1","expires_in":300,"token_type":"Bearer"}`))
	})

	tokens, err := c.Exchange(t.Context(), "auth-code-1", "verifier-1", "http://127.0.0.1:1234/callback")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if tokens.AccessToken != "at-1" || tokens.RefreshToken != "rt-1" || tokens.IDToken != "idt-1" || tokens.ExpiresIn != 300 {
		t.Fatalf("unexpected tokens: %+v", tokens)
	}

	if gotBody.Get("grant_type") != "authorization_code" {
		t.Fatalf("grant_type = %q, want authorization_code", gotBody.Get("grant_type"))
	}
	if gotBody.Get("client_id") != "pos-desktop" {
		t.Fatalf("client_id = %q, want pos-desktop", gotBody.Get("client_id"))
	}
	if gotBody.Get("client_secret") != "" {
		t.Fatal("client_secret must never be sent — pos-desktop is a public PKCE-only client")
	}
	if gotBody.Get("code") != "auth-code-1" {
		t.Fatalf("code = %q, want auth-code-1", gotBody.Get("code"))
	}
	if gotBody.Get("code_verifier") != "verifier-1" {
		t.Fatalf("code_verifier = %q, want verifier-1", gotBody.Get("code_verifier"))
	}
	if gotBody.Get("redirect_uri") != "http://127.0.0.1:1234/callback" {
		t.Fatalf("redirect_uri = %q, unexpected", gotBody.Get("redirect_uri"))
	}
}

func TestClient_Refresh_SendsRefreshTokenGrant(t *testing.T) {
	var gotBody url.Values
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotBody = r.Form
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-2","refresh_token":"rt-2","expires_in":300,"token_type":"Bearer"}`))
	})

	tokens, err := c.Refresh(t.Context(), "old-refresh-token")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tokens.AccessToken != "at-2" || tokens.RefreshToken != "rt-2" {
		t.Fatalf("unexpected tokens: %+v", tokens)
	}
	if gotBody.Get("grant_type") != "refresh_token" {
		t.Fatalf("grant_type = %q, want refresh_token", gotBody.Get("grant_type"))
	}
	if gotBody.Get("refresh_token") != "old-refresh-token" {
		t.Fatalf("refresh_token = %q, want old-refresh-token", gotBody.Get("refresh_token"))
	}
}

func TestClient_Refresh_PropagatesErrorOnInvalidGrant(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token is not active"}`))
	})

	_, err := c.Refresh(t.Context(), "expired-refresh-token")
	if err == nil {
		t.Fatal("Refresh: want error on invalid_grant, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("error = %v, want it to mention status 400", err)
	}
}
