package ws_test

// End-to-end kitchen WebSocket tests against a real Postgres (testcontainers)
// and a real NATS JetStream server (testcontainers, nats:2.12-alpine — same
// image as deploy/docker-compose.dev.yml — with JetStream enabled via
// Cmd:["-js"]).
//
// Scenario: connect -> receive snapshot -> place/transition a real order via
// OrderService (which writes to pos_outbox in the same transaction, exactly
// as production does) -> publish the event to NATS on the exact subject the
// outbox dispatcher emits (module.eventType.v1 — see
// platform/outbox/dispatcher.go toSubject; NOT the 5-token
// "<module>.<event>.<v>.<tenant_id>" scheme in docs/architecture.md, which
// the dispatcher does not actually implement) -> assert the WS client
// receives it.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tccore "github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/fx"
	"go.uber.org/goleak"
	"go.uber.org/zap"

	paymentpub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/pos/domain"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/modules/pos/service"
	"onlinemenu.tr/internal/modules/pos/ws"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
	"onlinemenu.tr/internal/platform/eventbus"
)

var (
	sharedPool *db.Pool
	natsURL    string

	kitchenRoleID = uuid.MustParse("00000001-0000-0000-0000-000000000004")
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	pgCtr, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres container: %v\n", err)
		os.Exit(1)
	}

	superDSN, err := pgCtr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "get connection string: %v\n", err)
		_ = pgCtr.Terminate(ctx)
		os.Exit(1)
	}

	if err := bootstrapRoles(ctx, superDSN); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap roles: %v\n", err)
		_ = pgCtr.Terminate(ctx)
		os.Exit(1)
	}
	if err := runMigrations(superDSN); err != nil {
		fmt.Fprintf(os.Stderr, "run migrations: %v\n", err)
		_ = pgCtr.Terminate(ctx)
		os.Exit(1)
	}
	sharedPool = newPool(ctx, superDSN, "app_runtime", "runtime_secret")

	natsCtr, err := tccore.GenericContainer(ctx, tccore.GenericContainerRequest{
		ContainerRequest: tccore.ContainerRequest{
			Image:        "nats:2.12-alpine",
			Cmd:          []string{"-js"},
			ExposedPorts: []string{"4222/tcp"},
			WaitingFor:   wait.ForListeningPort("4222/tcp").WithStartupTimeout(30 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "start nats container: %v\n", err)
		sharedPool.Close()
		_ = pgCtr.Terminate(ctx)
		os.Exit(1)
	}
	host, err := natsCtr.Host(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nats host: %v\n", err)
		os.Exit(1)
	}
	port, err := natsCtr.MappedPort(ctx, "4222/tcp")
	if err != nil {
		fmt.Fprintf(os.Stderr, "nats port: %v\n", err)
		os.Exit(1)
	}
	natsURL = fmt.Sprintf("nats://%s:%s", host, port.Port())

	rc := m.Run()

	sharedPool.Close()
	_ = pgCtr.Terminate(ctx)
	_ = natsCtr.Terminate(ctx)

	if err := goleak.Find(
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*DockerContainer).followOutput"),
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*DockerContainer).tailOrFollowOutput"),
		// nats.go's internal reconnect/flusher goroutines wind down
		// asynchronously after Conn.Close(); bounded by the library, not by
		// code under test here.
		goleak.IgnoreTopFunction("github.com/nats-io/nats.go.(*Conn).spinUpSocketWatchers.func1"),
	); err != nil {
		fmt.Fprintf(os.Stderr, "goleak: %v\n", err)
		rc = 1
	}

	os.Exit(rc)
}

// migrationsBase returns the absolute path to backend/migrations.
// File: .../backend/internal/modules/pos/ws/hub_test.go
// Walk up 4 directories: ws/ -> pos/ -> modules/ -> internal/ -> backend/
func migrationsBase() string {
	_, file, _, _ := runtime.Caller(0)
	base := filepath.Dir(file)
	for range 4 {
		base = filepath.Dir(base)
	}
	return filepath.Join(base, "migrations")
}

func runMigrations(superDSN string) error {
	cfg, err := pgxpool.ParseConfig(superDSN)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}
	migratorDSN := fmt.Sprintf("pgx5://%s:%s@%s/%s?sslmode=disable",
		"app_migrator", "migrator_secret",
		cfg.ConnConfig.Host+fmt.Sprintf(":%d", cfg.ConnConfig.Port),
		cfg.ConnConfig.Database,
	)

	for _, mod := range []string{"tenant", "identity", "catalog", "pos"} {
		absPath := filepath.Join(migrationsBase(), mod)
		src := fmt.Sprintf("file://%s", absPath)
		dsn := fmt.Sprintf("%s&x-migrations-table=schema_migrations_%s", migratorDSN, mod)

		m, err := migrate.New(src, dsn)
		if err != nil {
			return fmt.Errorf("migrate open %s: %w", mod, err)
		}
		if err := m.Up(); err != nil && err != migrate.ErrNoChange {
			m.Close()
			return fmt.Errorf("migrate up %s: %w", mod, err)
		}
		m.Close()
	}
	return nil
}

func bootstrapRoles(ctx context.Context, superDSN string) error {
	conn, err := pgx.Connect(ctx, superDSN)
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
			end := len(s)
			if end > 60 {
				end = 60
			}
			return fmt.Errorf("stmt failed %q: %w", s[:end], err)
		}
	}
	return nil
}

func newPool(ctx context.Context, baseDSN, user, password string) *db.Pool {
	cfg, err := pgxpool.ParseConfig(baseDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse pool config: %v\n", err)
		os.Exit(1)
	}
	cfg.ConnConfig.User = user
	cfg.ConnConfig.Password = password
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.MaxConns = 10

	p, err := db.NewPoolFromConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create pool (%s): %v\n", user, err)
		os.Exit(1)
	}
	return p
}

// zeroSaleReader satisfies paymentpub.SaleReader; CheckService is wired for
// completeness but these tests never close a check.
type zeroSaleReader struct{}

func (zeroSaleReader) TotalPaidForCheck(_ context.Context, _, _ uuid.UUID) (int64, error) {
	return 0, nil
}

var _ paymentpub.SaleReader = zeroSaleReader{}

// fakeLifecycle captures fx.Hooks so tests can invoke OnStart/OnStop
// directly without booting a full fx.App (fxtest is not a project
// dependency).
type fakeLifecycle struct {
	hooks []fx.Hook
}

func (f *fakeLifecycle) Append(h fx.Hook) { f.hooks = append(f.hooks, h) }

func (f *fakeLifecycle) start(ctx context.Context) error {
	for _, h := range f.hooks {
		if h.OnStart == nil {
			continue
		}
		if err := h.OnStart(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeLifecycle) stop(ctx context.Context) {
	for i := len(f.hooks) - 1; i >= 0; i-- {
		if f.hooks[i].OnStop == nil {
			continue
		}
		_ = f.hooks[i].OnStop(ctx)
	}
}

// testEnv wires one Hub + chi.Mux + OPA engine + eventbus.Bus against the
// shared Postgres pool and a fresh NATS connection/stream per test (fresh
// stream name per test avoids cross-test event replay on the durable
// consumer, which starts from DeliverAllPolicy on first creation).
type testEnv struct {
	hub    *ws.Hub
	bus    *eventbus.Bus
	orders *service.OrderService
	checks *service.CheckService
	mux    *chi.Mux
	lc     *fakeLifecycle
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	logger := zap.NewNop()

	orderRepo := repo.NewOrderRepo()
	checkRepo := repo.NewCheckRepo()
	orders := service.NewOrderService(service.OrderParams{DB: sharedPool, OrderRepo: orderRepo, Logger: logger})
	checks := service.NewCheckService(service.CheckParams{DB: sharedPool, CheckRepo: checkRepo, SaleReader: zeroSaleReader{}, Logger: logger})

	lc := &fakeLifecycle{}
	// Unique stream name per test, BUT JetStream also rejects a new stream
	// whose subject set ("pos.>") overlaps an existing stream regardless of
	// name — so the stream created here must be deleted in cleanup (see
	// deleteStream below), not just have the bus connection closed, or the
	// next test's CreateOrUpdateStream fails with "subjects overlap".
	streamName := "TEST_" + uuid.New().String()[:8]
	bus, err := eventbus.NewBus(lc, eventbus.Config{
		URL:        natsURL,
		StreamName: streamName,
		Subjects:   []string{"pos.>"},
	}, logger)
	require.NoError(t, err)
	// Registered immediately (before lc.start, which is what actually talks
	// to NATS and can fail) so a failure here still closes the connection
	// NewBus already opened, instead of leaking it.
	t.Cleanup(func() { lc.stop(context.Background()) })
	t.Cleanup(func() { deleteStream(t, streamName) })

	hub := ws.NewHub(ws.Params{Bus: bus, Orders: orders, Checks: checks, Logger: logger})
	hub.Register(lc)

	require.NoError(t, lc.start(context.Background()))

	engine, err := auth.NewEngine(
		auth.EngineConfig{BundlePath: "../../../../configs/opa/bundles"},
		redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond}),
		logger,
	)
	require.NoError(t, err)

	mux := chi.NewMux()
	hub.RegisterRoutes(mux, engine)

	return &testEnv{hub: hub, bus: bus, orders: orders, checks: checks, mux: mux, lc: lc}
}

// deleteStream removes a test's JetStream stream so the next test's
// overlapping "pos.>" subject filter does not collide with it (JetStream
// rejects a new stream whose subjects overlap an existing stream regardless
// of stream name). Uses its own short-lived connection since the testEnv's
// bus connection may already be closed (lc.stop runs after this, per
// t.Cleanup's LIFO order in newTestEnv).
func deleteStream(t *testing.T, streamName string) {
	t.Helper()
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Logf("deleteStream: connect: %v", err)
		return
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		t.Logf("deleteStream: jetstream context: %v", err)
		return
	}
	if err := js.DeleteStream(context.Background(), streamName); err != nil {
		t.Logf("deleteStream: delete %s: %v", streamName, err)
	}
}

// publish mimics platform/outbox.Dispatcher.publish: same subject format
// ("<module>.<eventType>.v1", NOT the tenant-suffixed 5-token scheme in
// docs/architecture.md) and the same Nats-Msg-Id dedup header.
func (e *testEnv) publish(t *testing.T, eventType string, payload map[string]any) {
	t.Helper()
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	err = e.bus.PublishMsg(context.Background(), &nats.Msg{
		Subject: "pos." + eventType + ".v1",
		Data:    data,
		Header:  nats.Header{"Nats-Msg-Id": []string{uuid.New().String()}},
	})
	require.NoError(t, err)
}

func dialKitchenWS(t *testing.T, srv *httptest.Server, token, branchID string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	url := "ws" + srv.URL[len("http"):] + ws.KitchenWSPath + "?branch_id=" + branchID
	return websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
}

// stubVerifier + a fake CTX-less bearer scheme: rather than minting real
// Keycloak/CTX tokens, tests inject auth.Principal directly via a tiny
// middleware ahead of the mux (see newAuthedServer) — RequirePermission and
// FromContext are what's under test here, not token verification itself
// (covered by platform/auth's own tests).
func newAuthedServer(t *testing.T, mux *chi.Mux, principals map[string]auth.Principal) *httptest.Server {
	t.Helper()
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(tok) > len(prefix) {
			tok = tok[len(prefix):]
		}
		p, ok := principals[tok]
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r = r.WithContext(auth.WithPrincipal(r.Context(), p))
		mux.ServeHTTP(w, r)
	})
	return httptest.NewServer(wrapped)
}

func readOne(t *testing.T, c *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := c.Read(ctx)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

// ---------------------------------------------------------------------------
// Scenarios
// ---------------------------------------------------------------------------

func TestKitchenWS_Snapshot_Then_LiveOrderEvents(t *testing.T) {
	env := newTestEnv(t)

	tenantID := uuid.New()
	branchID := uuid.New()

	principal := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: branchID,
		RoleIDs:  []uuid.UUID{kitchenRoleID},
	}
	srv := newAuthedServer(t, env.mux, map[string]auth.Principal{"kitchen-token": principal})
	defer srv.Close()

	// A dine-in check + order created BEFORE connecting, so the snapshot
	// (not a live event) must surface it.
	chk, err := env.checks.Open(context.Background(), tenantID, principal, domain.Check{
		BranchID:   branchID,
		TableLabel: "Masa 7",
		OpenedBy:   principal.PersonID,
	})
	require.NoError(t, err)

	order, err := env.orders.Place(context.Background(), tenantID, principal, domain.Order{
		BranchID:     branchID,
		CheckID:      &chk.ID,
		OrderChannel: domain.OrderChannelDineIn,
		Items: []domain.OrderItem{
			{ProductID: uuid.New(), ProductName: "Adana", ProductCurrency: "TRY", Quantity: 2, UnitPriceAmount: 15000},
		},
	})
	require.NoError(t, err)

	conn, _, err := dialKitchenWS(t, srv, "kitchen-token", branchID.String())
	require.NoError(t, err)
	defer conn.CloseNow()

	snapshot := readOne(t, conn, 5*time.Second)
	require.Equal(t, "snapshot", snapshot["type"])
	orders, _ := snapshot["orders"].([]any)
	require.Len(t, orders, 1, "the pre-existing order must appear in the snapshot")
	row := orders[0].(map[string]any)
	require.Equal(t, order.ID.String(), row["order_id"])
	require.Equal(t, "Masa 7", row["table_label"])
	require.Equal(t, "pending", row["status"])
	require.Equal(t, "order.placed", row["type"])

	// Live event: publish the order.placed event the outbox dispatcher would
	// have emitted for this same order (already created above — this
	// exercises the live broadcast path, not creation).
	env.publish(t, "order.placed", map[string]any{
		"tenant_id":     tenantID.String(),
		"order_id":      order.ID.String(),
		"branch_id":     branchID.String(),
		"check_id":      chk.ID.String(),
		"order_channel": "dine_in",
		"item_count":    1,
	})

	msg := readOne(t, conn, 5*time.Second)
	require.Equal(t, "order.placed", msg["type"])
	require.Equal(t, order.ID.String(), msg["order_id"])
	require.Equal(t, "Masa 7", msg["table_label"])
	require.Equal(t, "pending", msg["status"])
	require.NotEmpty(t, msg["seq"], "live events must carry the JetStream stream sequence")
	require.NotEmpty(t, msg["occurred_at"])

	// Accept the order, then publish order.accepted — must normalize to
	// order.status_changed with the reloaded (not payload-supplied) status.
	_, err = env.orders.Accept(context.Background(), tenantID, principal, order.ID, principal.PersonID)
	require.NoError(t, err)
	env.publish(t, "order.accepted", map[string]any{
		"tenant_id":   tenantID.String(),
		"order_id":    order.ID.String(),
		"accepted_by": principal.PersonID.String(),
	})

	msg = readOne(t, conn, 5*time.Second)
	require.Equal(t, "order.status_changed", msg["type"])
	require.Equal(t, "accepted", msg["status"])
	require.Equal(t, "Masa 7", msg["table_label"])
}

func TestKitchenWS_NoPermission_Forbidden(t *testing.T) {
	env := newTestEnv(t)

	tenantID, branchID := uuid.New(), uuid.New()
	roleless := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: branchID,
		// RoleIDs intentionally empty.
	}
	srv := newAuthedServer(t, env.mux, map[string]auth.Principal{"roleless-token": roleless})
	defer srv.Close()

	_, resp, err := dialKitchenWS(t, srv, "roleless-token", branchID.String())
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestKitchenWS_ForeignBranch_Forbidden(t *testing.T) {
	env := newTestEnv(t)

	tenantID := uuid.New()
	ownBranch := uuid.New()
	otherBranch := uuid.New()
	principal := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: ownBranch, // kitchen role is branch-scoped, not chain-wide
		RoleIDs:  []uuid.UUID{kitchenRoleID},
	}
	srv := newAuthedServer(t, env.mux, map[string]auth.Principal{"kitchen-token": principal})
	defer srv.Close()

	_, resp, err := dialKitchenWS(t, srv, "kitchen-token", otherBranch.String())
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestKitchenWS_MissingBranchID_UnprocessableEntity(t *testing.T) {
	env := newTestEnv(t)

	tenantID, branchID := uuid.New(), uuid.New()
	principal := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: branchID,
		RoleIDs:  []uuid.UUID{kitchenRoleID},
	}
	srv := newAuthedServer(t, env.mux, map[string]auth.Principal{"kitchen-token": principal})
	defer srv.Close()

	_, resp, err := dialKitchenWS(t, srv, "kitchen-token", "")
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

func TestKitchenWS_HeartbeatTimeout_DropsConnection(t *testing.T) {
	env := newTestEnv(t)
	env.hub.SetTimingForTest(30*time.Millisecond, 50*time.Millisecond, time.Second)

	tenantID, branchID := uuid.New(), uuid.New()
	principal := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: branchID,
		RoleIDs:  []uuid.UUID{kitchenRoleID},
	}
	srv := newAuthedServer(t, env.mux, map[string]auth.Principal{"kitchen-token": principal})
	defer srv.Close()

	conn, _, err := dialKitchenWS(t, srv, "kitchen-token", branchID.String())
	require.NoError(t, err)
	defer conn.CloseNow()

	// Read the snapshot once, then stop reading entirely: with no client-side
	// reader running, subsequent server pings go unanswered.
	_ = readOne(t, conn, 5*time.Second)

	require.Eventually(t, func() bool {
		return env.hub.RoomSizeForTest(tenantID, branchID) == 0
	}, 3*time.Second, 20*time.Millisecond, "an unresponsive connection must be dropped from its room after heartbeat timeout")
}
