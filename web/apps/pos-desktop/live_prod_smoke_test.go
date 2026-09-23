//go:build liveprod

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
	"onlinemenu.tr/pos-desktop/internal/apiclient"
	"onlinemenu.tr/pos-desktop/internal/config"
	"onlinemenu.tr/pos-desktop/internal/keycloakauth"
	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// Read-only production smoke: cashier token via the e2e-prod direct-grant
// client, then the same App bindings the webview calls. It never writes.
//
//	task pos:test:liveprod   (E2E_PROD_CLIENT_ID/SECRET, PROD_SMOKE_EMAIL/PASSWORD; skips when unset)
func TestLiveProdReadOnly(t *testing.T) {
	clientID, secret := os.Getenv("E2E_PROD_CLIENT_ID"), os.Getenv("E2E_PROD_CLIENT_SECRET")
	user, pass := os.Getenv("PROD_SMOKE_EMAIL"), os.Getenv("PROD_SMOKE_PASSWORD")
	if clientID == "" || secret == "" || user == "" || pass == "" {
		t.Skip("prod smoke credentials not set")
	}
	keyring.MockInit()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.APIBaseURL, "https://") {
		t.Fatalf("refusing to run against non-prod defaults (api=%s): use task pos:test:liveprod", cfg.APIBaseURL)
	}

	form := url.Values{"grant_type": {"password"}, "client_id": {clientID}, "client_secret": {secret},
		"username": {user}, "password": {pass}, "scope": {"openid"}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", cfg.KeycloakURL, cfg.KeycloakRealm), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil || tok.AccessToken == "" {
		t.Fatalf("keycloak token: status %d err %v", resp.StatusCode, err)
	}

	dir := t.TempDir()
	a := &App{ctx: ctx, api: apiclient.New(cfg.APIBaseURL, tokenstore.New(dir, nil)),
		kc:      keycloakauth.New(keycloakauth.Config{BaseURL: cfg.KeycloakURL, Realm: cfg.KeycloakRealm, ClientID: keycloakClientID}),
		kcStore: tokenstore.NewKeycloak(dir, nil), openURL: func(string) {}}
	if err := a.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	res, err := a.resolveContexts(tok.AccessToken)
	if err != nil {
		t.Fatalf("resolveContexts: %v", err)
	}
	sess := res.Session
	if res.NeedsContextSelection {
		if sess, err = a.completeContextSelection(tok.AccessToken, res.Contexts[0].MembershipID); err != nil {
			t.Fatalf("select context: %v", err)
		}
	}
	if sess.BranchID == "" {
		t.Fatalf("session has no branch: %+v", sess)
	}

	cats, err := a.ListCategories()
	if err != nil || len(cats) == 0 {
		t.Fatalf("ListCategories: %d, %v", len(cats), err)
	}
	var products []ProductDTO
	for _, c := range cats {
		if products, err = a.ListProducts(c.ID); err != nil {
			t.Fatalf("ListProducts(%s): %v", c.ID, err)
		}
		if len(products) > 0 {
			break
		}
	}
	if len(products) == 0 {
		t.Fatal("no category has products")
	}
	zones, err := a.ListTables(sess.BranchID)
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	tables := 0
	for _, z := range zones {
		tables += len(z.Tables)
	}
	if tables == 0 {
		t.Fatal("no tables")
	}
	groups, err := a.ListProductModifierGroups(products[0].ID)
	if err != nil {
		t.Fatalf("ListProductModifierGroups: %v", err)
	}
	t.Logf("api=%s branch=%s categories=%d products=%d tables=%d modifierGroups(%s)=%d",
		cfg.APIBaseURL, sess.BranchID, len(cats), len(products), tables, products[0].Name, len(groups))
}
