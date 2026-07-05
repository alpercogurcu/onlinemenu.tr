package keycloakauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Config identifies the Keycloak realm/client this package talks to.
type Config struct {
	BaseURL  string // e.g. http://localhost:8090
	Realm    string // "onlinemenu"
	ClientID string // "pos-desktop" — public client, PKCE-only, no secret
}

// Tokens is the subset of a Keycloak token endpoint response this client
// needs. AccessToken/IDToken must only ever be kept in memory by callers —
// RefreshToken is the only field the caller (main.App) persists, to the OS
// keychain (see pos-desktop/README.md, "Keychain içeriği").
type Tokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresIn    int
}

// Client calls the Keycloak realm's OIDC authorize/token/end_session
// endpoints directly — NOT the onlinemenu.tr backend. It is deliberately
// separate from apiclient.Client (the backend's single HTTP authority, see
// pos-desktop/README.md): backend calls that are part of the login
// sequence (me/contexts, auth/context) still go exclusively through
// apiclient.Client; this type never touches the backend and apiclient never
// touches Keycloak's IdP endpoints. This split keeps the "no second
// interceptor can race the backend CTX-token refresh" guarantee intact —
// there is exactly one code path (apiclient.Client) that reads/writes the
// backend token, and it is unaffected by this client existing.
type Client struct {
	cfg        Config
	httpClient *http.Client
}

// New constructs a Client for cfg.
func New(cfg Config) *Client {
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) realmURL() string {
	return fmt.Sprintf("%s/realms/%s", c.cfg.BaseURL, c.cfg.Realm)
}

func (c *Client) authorizationEndpoint() string {
	return c.realmURL() + "/protocol/openid-connect/auth"
}

func (c *Client) tokenEndpoint() string {
	return c.realmURL() + "/protocol/openid-connect/token"
}

func (c *Client) endSessionEndpoint() string {
	return c.realmURL() + "/protocol/openid-connect/logout"
}

// AuthorizeURLParams bundles the per-attempt values the authorize request
// needs — all freshly generated (see pkce.go) for every login attempt.
type AuthorizeURLParams struct {
	RedirectURI   string
	State         string
	Nonce         string
	CodeChallenge string
}

// AuthorizeURL builds the URL to open in the system browser (see
// runtime.BrowserOpenURL in main.App.LoginWithKeycloak).
func (c *Client) AuthorizeURL(p AuthorizeURLParams) string {
	q := url.Values{
		"client_id":             {c.cfg.ClientID},
		"response_type":         {"code"},
		"scope":                 {"openid"},
		"redirect_uri":          {p.RedirectURI},
		"state":                 {p.State},
		"nonce":                 {p.Nonce},
		"code_challenge":        {p.CodeChallenge},
		"code_challenge_method": {"S256"},
	}
	return c.authorizationEndpoint() + "?" + q.Encode()
}

// EndSessionURL builds a best-effort logout URL for the system browser
// (main.App.Logout). idTokenHint may be empty — the ID token is never
// retained past the login flow (see pos-desktop/README.md, "Keychain
// içeriği"), so a station that restarted and then logs out has none to
// hint with; Keycloak's frontchannel logout still clears its own session
// cookie for the current browser without it, just without an explicit
// hint. Failure to open the browser is not fatal to local logout — the
// local session is already cleared by the caller before this URL is
// opened.
func (c *Client) EndSessionURL(idTokenHint string) string {
	q := url.Values{"client_id": {c.cfg.ClientID}}
	if idTokenHint != "" {
		q.Set("id_token_hint", idTokenHint)
	}
	return c.endSessionEndpoint() + "?" + q.Encode()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// Exchange performs the authorization_code + PKCE grant — no client secret,
// since "pos-desktop" is a public client (directAccessGrantsEnabled=false,
// PKCE S256 required — see deploy/keycloak/realm-onlinemenu.json).
func (c *Client) Exchange(ctx context.Context, code, verifier, redirectURI string) (Tokens, error) {
	body := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {c.cfg.ClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	}
	return c.postToken(ctx, body)
}

// Refresh performs the refresh_token grant — used both for the app-startup
// silent restore (main.App.TryRestoreSession) and the CTX-401 recovery hook
// (main.App.recoverKeycloakContext).
func (c *Client) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	body := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.cfg.ClientID},
		"refresh_token": {refreshToken},
	}
	return c.postToken(ctx, body)
}

func (c *Client) postToken(ctx context.Context, body url.Values) (Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenEndpoint(), bytes.NewBufferString(body.Encode()))
	if err != nil {
		return Tokens{}, fmt.Errorf("keycloakauth: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Tokens{}, fmt.Errorf("keycloakauth: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return Tokens{}, fmt.Errorf("keycloakauth: token endpoint responded %d: %s", resp.StatusCode, string(respBody))
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return Tokens{}, fmt.Errorf("keycloakauth: decode token response: %w", err)
	}
	return Tokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
		ExpiresIn:    tr.ExpiresIn,
	}, nil
}
