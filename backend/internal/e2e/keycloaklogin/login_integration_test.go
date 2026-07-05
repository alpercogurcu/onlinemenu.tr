//go:build keycloak_integration

// Package keycloaklogin_test proves, end-to-end against a REAL Keycloak, that a
// Keycloak-issued JWT flows through the platform auth stack exactly as the unit
// tests (which use a fake JWKS) claim it does, and that the ADR-AUTH-001
// two-stage login bridge works:
//
//	Keycloak JWT (sub only) -> auth.Middleware -> KeycloakVerifier
//	    -> Principal{KeycloakSub}
//	    -> identity /me/contexts + SelectContext (persons.keycloak_sub -> memberships)
//	    -> platform-signed CTX token -> Principal{TenantID, BranchID, RoleIDs}
//
// IMPORTANT (headline finding): the Keycloak access token carries ONLY `sub`
// for authorization purposes. tenant_id / branch_ids / roles are resolved from
// the database, NOT from JWT claims. See deploy/keycloak/README.md.
//
// This suite is slow (spins up Keycloak + Postgres) and is gated behind the
// `keycloak_integration` build tag so `go test ./...` stays fast. Run with:
//
//	go test -tags keycloak_integration ./internal/e2e/keycloaklogin/...
package keycloaklogin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tc "github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"

	identityhttp "onlinemenu.tr/internal/modules/identity/http"
	identityrepo "onlinemenu.tr/internal/modules/identity/repo"
	identitysvc "onlinemenu.tr/internal/modules/identity/service"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// Fixtures shared by every subtest. tenantID/branchID mirror the dev-cashier
// user attributes in realm-onlinemenu.json and the platform dev seed.
var (
	tenantID    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	branchID    = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")
	managerRole = uuid.MustParse("00000001-0000-0000-0000-000000000006") // seeded system role "manager"

	// Backend audience: injected by the onlinemenu-audience client scope's
	// audience mapper. The verifier asserts `aud` contains this value.
	backendAudience = "onlinemenu-backend"

	devUser     = "dev-cashier"
	devPassword = "Passw0rd!"
)

var (
	kcBaseURL   string  // e.g. http://localhost:32769
	kcIssuer    string  // kcBaseURL + /realms/onlinemenu
	sharedPool  *db.Pool
	ctxSigner   *auth.ContextTokenSigner
	personSvc   *identitysvc.PersonService
	membSvc     *identitysvc.MembershipService
	contextSvc  *identitysvc.ContextService
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	kcTerminate, err := startKeycloak(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start keycloak: %v\n", err)
		os.Exit(1)
	}
	defer kcTerminate()

	pgTerminate, err := startPostgres(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres: %v\n", err)
		kcTerminate()
		os.Exit(1)
	}
	defer pgTerminate()

	code := m.Run()

	sharedPool.Close()
	pgTerminate()
	kcTerminate()
	os.Exit(code)
}

// -----------------------------------------------------------------------------
// The actual proof.
// -----------------------------------------------------------------------------

// TestKeycloakJWTVerifiesAndBridgesToContextToken is the core end-to-end proof
// (task instruction #2 + #3 merged). It uses a real Keycloak token, real JWKS,
// and a real Postgres-backed identity module.
func TestKeycloakJWTVerifiesAndBridgesToContextToken(t *testing.T) {
	ctx := context.Background()

	rawToken, err := passwordGrant(ctx, "onlinemenu-dev-cli", devUser, devPassword)
	require.NoError(t, err, "password grant against real Keycloak must succeed")

	sub := decodeSub(t, rawToken)
	require.NotEmpty(t, sub, "access token must carry `sub` (requires the built-in `basic` client scope)")

	// Sanity: confirm the token's audience contains our backend audience. This
	// is exactly what the fake-JWKS unit tests cannot prove — that the REAL
	// Keycloak realm is configured to emit an `aud` the verifier accepts.
	assertAudienceContains(t, rawToken, backendAudience)

	// Seed the DB bridge: persons.keycloak_sub MUST equal the token `sub`.
	seedPersonAndMembership(t, ctx, sub)

	// --- Stage 1: Keycloak JWT -> Middleware -> KeycloakVerifier -> Principal ---
	verifier, err := auth.NewKeycloakVerifier(ctx, auth.KeycloakVerifierConfig{
		IssuerURL: kcIssuer,
		Audience:  backendAudience,
	})
	require.NoError(t, err, "verifier must fetch JWKS from real Keycloak")

	principal := runMiddleware(t, verifier, rawToken)
	require.True(t, principal.IsPreContext(), "Keycloak token yields a pre-context principal")
	assert.Equal(t, sub, principal.KeycloakSub, "verifier maps the `sub` claim onto Principal.KeycloakSub")
	assert.Equal(t, uuid.Nil, principal.PersonID, "no person/tenant/role context from the JWT alone")

	// Mount the real identity Handler behind auth.Middleware + KeycloakVerifier.
	// roleSvc and the OPA engine are nil: /me/contexts and /auth/context are the
	// pre-context flow (routes.go) gated by auth.Middleware only — no permit()
	// wrapper is invoked for them, so the engine is never dereferenced.
	handler := identityhttp.NewHandler(personSvc, nil, membSvc, contextSvc, zap.NewNop(), nil)
	mux := chi.NewMux()
	mux.Use(auth.Middleware(verifier, ctxSigner))
	handler.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// --- Stage 2: real HTTP GET /v1/identity/me/contexts with the Keycloak token ---
	membershipID, ctxTenant := httpListContexts(t, srv.URL, rawToken)
	assert.Equal(t, tenantID, ctxTenant, "tenant surfaced by /me/contexts")

	// --- Stage 3: real HTTP POST /v1/identity/auth/context -> platform CTX token ---
	ctxToken := httpSelectContext(t, srv.URL, rawToken, membershipID)

	// The CTX token is what actually carries authz context. Verify it and assert
	// tenant/branch/roles — sourced from the DB, not the JWT.
	ctxPrincipal, err := ctxSigner.Verify(ctxToken)
	require.NoError(t, err)
	assert.True(t, ctxPrincipal.IsStaff())
	assert.Equal(t, tenantID, ctxPrincipal.TenantID, "tenant_id resolved from DB membership")
	assert.Equal(t, branchID, ctxPrincipal.BranchID, "branch_id resolved from DB membership")
	assert.Contains(t, ctxPrincipal.RoleIDs, managerRole, "roles resolved from DB membership")
}

// TestWrongAudienceRejected proves aud enforcement against a real token.
func TestWrongAudienceRejected(t *testing.T) {
	ctx := context.Background()

	rawToken, err := passwordGrant(ctx, "onlinemenu-dev-cli", devUser, devPassword)
	require.NoError(t, err)

	verifier, err := auth.NewKeycloakVerifier(ctx, auth.KeycloakVerifierConfig{
		IssuerURL: kcIssuer,
		Audience:  "some-other-service", // not present in the token's aud
	})
	require.NoError(t, err)

	status := runMiddlewareStatus(t, verifier, rawToken)
	assert.Equal(t, http.StatusUnauthorized, status, "token with mismatched audience must be rejected")
}

// TestExpiredTokenRejected proves exp enforcement against a real, naturally
// expired token (dev-shortlived client sets access.token.lifespan=1s).
func TestExpiredTokenRejected(t *testing.T) {
	ctx := context.Background()

	rawToken, err := passwordGrant(ctx, "onlinemenu-dev-shortlived", devUser, devPassword)
	require.NoError(t, err)

	// Build the verifier BEFORE sleeping so JWKS is cached; then let the token expire.
	verifier, err := auth.NewKeycloakVerifier(ctx, auth.KeycloakVerifierConfig{
		IssuerURL: kcIssuer,
		Audience:  backendAudience,
	})
	require.NoError(t, err)

	time.Sleep(3 * time.Second) // > 1s lifespan; no JWT leeway configured

	status := runMiddlewareStatus(t, verifier, rawToken)
	assert.Equal(t, http.StatusUnauthorized, status, "expired token must be rejected")
}

// -----------------------------------------------------------------------------
// HTTP middleware harness
// -----------------------------------------------------------------------------

func runMiddleware(t *testing.T, verifier auth.TokenVerifier, rawToken string) auth.Principal {
	t.Helper()
	var captured auth.Principal
	handler := auth.Middleware(verifier, ctxSigner)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := auth.FromContext(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		captured = p
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "middleware should accept the token")
	return captured
}

func runMiddlewareStatus(t *testing.T, verifier auth.TokenVerifier, rawToken string) int {
	t.Helper()
	handler := auth.Middleware(verifier, ctxSigner)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}

// httpListContexts fires a real GET /v1/identity/me/contexts with the Keycloak
// bearer token and returns the single membership's id + tenant.
func httpListContexts(t *testing.T, baseURL, bearer string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/identity/me/contexts", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+bearer)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "/me/contexts must accept the Keycloak token")

	var body struct {
		Contexts []struct {
			MembershipID uuid.UUID `json:"membership_id"`
			TenantID     uuid.UUID `json:"tenant_id"`
		} `json:"contexts"`
		Customer bool `json:"customer"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Contexts, 1, "seeded person has exactly one membership")
	return body.Contexts[0].MembershipID, body.Contexts[0].TenantID
}

// httpSelectContext fires a real POST /v1/identity/auth/context and returns the
// platform-signed CTX token.
func httpSelectContext(t *testing.T, baseURL, bearer string, membershipID uuid.UUID) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"membership_id": membershipID})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/identity/auth/context", bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "/auth/context must issue a CTX token")

	var body struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.NotEmpty(t, body.Token)
	return body.Token
}

// -----------------------------------------------------------------------------
// Keycloak helpers
// -----------------------------------------------------------------------------

func startKeycloak(ctx context.Context) (func(), error) {
	realmPath := filepath.Join(repoBackendDir(), "..", "deploy", "keycloak", "realm-onlinemenu.json")
	if _, err := os.Stat(realmPath); err != nil {
		return nil, fmt.Errorf("realm file not found at %s: %w", realmPath, err)
	}

	req := tc.ContainerRequest{
		Image:        "quay.io/keycloak/keycloak:26.2",
		ExposedPorts: []string{"8080/tcp"},
		Env: map[string]string{
			"KEYCLOAK_ADMIN":          "admin",
			"KEYCLOAK_ADMIN_PASSWORD": "admin",
			"KC_HTTP_ENABLED":         "true",
			"KC_HOSTNAME_STRICT":      "false",
			"KC_HEALTH_ENABLED":       "true",
		},
		Cmd: []string{"start-dev", "--import-realm"},
		Files: []tc.ContainerFile{{
			HostFilePath:      realmPath,
			ContainerFilePath: "/opt/keycloak/data/import/realm-onlinemenu.json",
			FileMode:          0o644,
		}},
		// The well-known endpoint only returns 200 once the realm is imported —
		// a stronger signal than a generic port check.
		WaitingFor: wait.ForHTTP("/realms/onlinemenu/.well-known/openid-configuration").
			WithPort("8080/tcp").
			WithStartupTimeout(180 * time.Second),
	}

	ctr, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("keycloak container: %w", err)
	}
	terminate := func() { _ = ctr.Terminate(context.Background()) }

	host, err := ctr.Host(ctx)
	if err != nil {
		terminate()
		return nil, err
	}
	port, err := ctr.MappedPort(ctx, "8080/tcp")
	if err != nil {
		terminate()
		return nil, err
	}
	kcBaseURL = fmt.Sprintf("http://%s:%s", host, port.Port())
	kcIssuer = kcBaseURL + "/realms/onlinemenu"
	return terminate, nil
}

// passwordGrant obtains an access token via the Resource Owner Password
// Credentials grant. Used ONLY here — real clients use Authorization Code + PKCE
// (Wave 2/3). The dev-only clients enable direct access grants; PKCE clients do not.
func passwordGrant(ctx context.Context, clientID, username, password string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", clientID)
	form.Set("username", username)
	form.Set("password", password)
	form.Set("scope", "openid")

	endpoint := kcIssuer + "/protocol/openid-connect/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}

	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("empty access_token")
	}
	return body.AccessToken, nil
}

func decodeSub(t *testing.T, rawToken string) string {
	t.Helper()
	claims := parseUnverified(t, rawToken)
	sub, _ := claims["sub"].(string)
	return sub
}

func assertAudienceContains(t *testing.T, rawToken, want string) {
	t.Helper()
	claims := parseUnverified(t, rawToken)
	switch aud := claims["aud"].(type) {
	case string:
		assert.Equal(t, want, aud, "aud claim")
	case []interface{}:
		var got []string
		for _, a := range aud {
			if s, ok := a.(string); ok {
				got = append(got, s)
			}
		}
		assert.Contains(t, got, want, "aud claim array must contain the backend audience")
	default:
		t.Fatalf("token has no usable aud claim: %T", claims["aud"])
	}
}

func parseUnverified(t *testing.T, rawToken string) jwt.MapClaims {
	t.Helper()
	claims := jwt.MapClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(rawToken, claims)
	require.NoError(t, err)
	return claims
}

// -----------------------------------------------------------------------------
// Postgres helpers (mirrors internal/e2e/spine_test.go setup)
// -----------------------------------------------------------------------------

func startPostgres(ctx context.Context) (func(), error) {
	ctr, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("run postgres: %w", err)
	}
	terminate := func() { _ = ctr.Terminate(context.Background()) }

	superDSN, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		terminate()
		return nil, err
	}
	if err := bootstrapRoles(ctx, superDSN); err != nil {
		terminate()
		return nil, err
	}
	if err := runMigrations(superDSN); err != nil {
		terminate()
		return nil, err
	}

	sharedPool = newPool(ctx, superDSN)
	buildIdentityServices()
	return terminate, nil
}

func buildIdentityServices() {
	log := zap.NewNop()

	secret := []byte("integration-test-context-token-secret-32b!!")
	var err error
	ctxSigner, err = auth.NewContextTokenSigner(secret)
	if err != nil {
		panic(err)
	}

	personRepo := identityrepo.NewPersonRepo()
	membershipRepo := identityrepo.NewMembershipRepo()
	roleRepo := identityrepo.NewRoleRepo()

	personSvc = identitysvc.NewPersonService(identitysvc.PersonParams{
		DB: sharedPool, PersonRepo: personRepo, Logger: log,
	})
	membSvc = identitysvc.NewMembershipService(identitysvc.MembershipParams{
		DB: sharedPool, MembershipRepo: membershipRepo, RoleRepo: roleRepo, Logger: log,
	})
	contextSvc = identitysvc.NewContextService(identitysvc.ContextParams{
		DB: sharedPool, MembershipRepo: membershipRepo, Signer: ctxSigner, Logger: log,
	})
}

// seedPersonAndMembership inserts the tenant, branch, person (keycloak_sub = the
// real token's sub) and an active manager membership. Uses the superuser
// connection which bypasses RLS.
func seedPersonAndMembership(t *testing.T, ctx context.Context, sub string) {
	t.Helper()
	// The pool connects as app_runtime (RLS-enforced); seed via a superuser conn.
	superConn := connectSuper(t, ctx)
	defer superConn.Close(ctx)

	personID := uuid.New()
	membershipID := uuid.New()

	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tenants (id, name, slug, plan, enabled_modules, is_active)
		  VALUES ($1, 'KC Test Restoran', 'kc-test-restoran', 'starter', '["pos","identity"]'::jsonb, TRUE)
		  ON CONFLICT (id) DO NOTHING`, []any{tenantID}},
		{`INSERT INTO branches (id, tenant_id, name, is_active)
		  VALUES ($1, $2, 'Ana Şube', TRUE) ON CONFLICT (id) DO NOTHING`, []any{branchID, tenantID}},
		{`INSERT INTO persons (id, keycloak_sub, email, full_name)
		  VALUES ($1, $2, 'cashier@dev.onlinemenu.tr', 'Dev Cashier')
		  ON CONFLICT (keycloak_sub) DO NOTHING`, []any{personID, sub}},
		{`INSERT INTO memberships (id, person_id, tenant_id, branch_id, role_id, status)
		  VALUES ($1, $2, $3, $4, $5, 'active')
		  ON CONFLICT (person_id, tenant_id, branch_id, role_id) DO NOTHING`,
			[]any{membershipID, personID, tenantID, branchID, managerRole}},
	}
	for _, s := range stmts {
		_, err := superConn.Exec(ctx, s.sql, s.args...)
		require.NoError(t, err, "seed stmt: %s", s.sql)
	}
}

func connectSuper(t *testing.T, ctx context.Context) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(ctx, superDSN)
	require.NoError(t, err)
	return conn
}

// -----------------------------------------------------------------------------
// DB bootstrap (copied pattern from spine_test.go)
// -----------------------------------------------------------------------------

var superDSN string

func repoBackendDir() string {
	_, file, _, _ := runtime.Caller(0)
	// file = .../backend/internal/e2e/keycloaklogin/login_integration_test.go
	// walk up 3: keycloaklogin -> e2e -> internal -> backend
	base := filepath.Dir(file)
	for range 3 {
		base = filepath.Dir(base)
	}
	return base
}

func migrationsBase() string { return filepath.Join(repoBackendDir(), "migrations") }

func runMigrations(dsn string) error {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}
	migratorDSN := fmt.Sprintf("pgx5://%s:%s@%s:%d/%s?sslmode=disable",
		"app_migrator", "migrator_secret",
		cfg.ConnConfig.Host, cfg.ConnConfig.Port, cfg.ConnConfig.Database)

	for _, mod := range []string{"tenant", "identity"} {
		src := "file://" + filepath.Join(migrationsBase(), mod)
		dsn := fmt.Sprintf("%s&x-migrations-table=schema_migrations_%s", migratorDSN, mod)
		mg, err := migrate.New(src, dsn)
		if err != nil {
			return fmt.Errorf("migrate open %s: %w", mod, err)
		}
		if err := mg.Up(); err != nil && err != migrate.ErrNoChange {
			mg.Close()
			return fmt.Errorf("migrate up %s: %w", mod, err)
		}
		mg.Close()
	}
	return nil
}

func bootstrapRoles(ctx context.Context, dsn string) error {
	superDSN = dsn
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	stmts := []string{
		`DO $$ BEGIN CREATE ROLE app_migrator LOGIN PASSWORD 'migrator_secret' BYPASSRLS;
		 EXCEPTION WHEN duplicate_object THEN NULL; END $$`,
		`DO $$ BEGIN CREATE ROLE app_runtime LOGIN PASSWORD 'runtime_secret' NOINHERIT;
		 EXCEPTION WHEN duplicate_object THEN NULL; END $$`,
		`GRANT USAGE ON SCHEMA public TO app_migrator, app_runtime`,
		`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`,
		`CREATE EXTENSION IF NOT EXISTS vector`,
		`ALTER SCHEMA public OWNER TO app_migrator`,
		`GRANT ALL ON SCHEMA public TO app_migrator`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE app_migrator IN SCHEMA public
		 GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_runtime`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE app_migrator IN SCHEMA public
		 GRANT USAGE ON SEQUENCES TO app_runtime`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			return fmt.Errorf("stmt failed: %w", err)
		}
	}
	return nil
}

func newPool(ctx context.Context, dsn string) *db.Pool {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		panic(err)
	}
	cfg.ConnConfig.User = "app_runtime"
	cfg.ConnConfig.Password = "runtime_secret"
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.MaxConns = 5

	p, err := db.NewPoolFromConfig(ctx, cfg)
	if err != nil {
		panic(err)
	}
	return p
}
