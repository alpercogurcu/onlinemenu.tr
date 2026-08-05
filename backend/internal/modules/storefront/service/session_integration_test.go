package service_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.uber.org/zap"

	pospub "onlinemenu.tr/internal/modules/pos/public"
	pub "onlinemenu.tr/internal/modules/storefront/public"
	"onlinemenu.tr/internal/modules/storefront/repo"
	"onlinemenu.tr/internal/modules/storefront/service"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// ResolveToken is the single most exposed code path in the product: it runs
// before any tenant is known, on input a stranger controls. Its RLS half is
// covered by platform/db's TestRLSQRTokenLookupIsRowScoped; what is covered
// here is the service decision layered on top — revoked codes, other tenants'
// codes and stale stickers must all be indistinguishable 404s to the caller.

var sharedPool *db.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx,
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

	superDSN, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}
	if err := bootstrapRoles(ctx, superDSN); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap roles: %v\n", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}
	if err := runMigrations(superDSN); err != nil {
		fmt.Fprintf(os.Stderr, "migrations: %v\n", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}

	sharedPool = newPool(ctx, superDSN, "app_runtime", "runtime_secret")
	rc := m.Run()
	sharedPool.Close()
	_ = ctr.Terminate(ctx)
	os.Exit(rc)
}

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
	migratorDSN := fmt.Sprintf("pgx5://app_migrator:migrator_secret@%s:%d/%s?sslmode=disable",
		cfg.ConnConfig.Host, cfg.ConnConfig.Port, cfg.ConnConfig.Database)

	// pos is not migrated here: table existence is validated through
	// pos/public, which this package may not reach past (go-arch-lint), so the
	// reader is a stub and no pos table is ever queried.
	for _, mod := range []string{"tenant", "identity", "storefront"} {
		src := "file://" + filepath.Join(migrationsBase(), mod)
		dsn := fmt.Sprintf("%s&x-migrations-table=schema_migrations_%s", migratorDSN, mod)
		mig, err := migrate.New(src, dsn)
		if err != nil {
			return fmt.Errorf("migrate open %s: %w", mod, err)
		}
		if err := mig.Up(); err != nil && err != migrate.ErrNoChange {
			mig.Close()
			return fmt.Errorf("migrate up %s: %w", mod, err)
		}
		mig.Close()
	}
	return nil
}

func bootstrapRoles(ctx context.Context, superDSN string) error {
	conn, err := pgx.Connect(ctx, superDSN)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	for _, s := range []string{
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
	} {
		if _, err := conn.Exec(ctx, s); err != nil {
			return fmt.Errorf("stmt: %w", err)
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
	cfg.MaxConns = 5

	p, err := db.NewPoolFromConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create pool: %v\n", err)
		os.Exit(1)
	}
	return p
}

// ---------------------------------------------------------------------------

// stubTables answers the pos/public table lookup. The storefront may not
// import pos internals (go-arch-lint), and what is under test is the
// storefront's reaction to each answer, not pos's ability to produce it.
type stubTables struct {
	table pospub.GuestTable
	err   error
}

func (s stubTables) GetGuestTable(_ context.Context, _, tableID uuid.UUID) (pospub.GuestTable, error) {
	if s.err != nil {
		return pospub.GuestTable{}, s.err
	}
	t := s.table
	if t.ID == uuid.Nil {
		t.ID = tableID
	}
	return t, nil
}

type qrSeed struct {
	tenantID uuid.UUID
	branchID uuid.UUID
	tableID  uuid.UUID
	rawToken string
}

// seedQRCode inserts an active QR code and returns its raw token. It goes
// through QRService so the token hashing under test is the production one.
func seedQRCode(t *testing.T, tenantID uuid.UUID) qrSeed {
	t.Helper()
	seed := qrSeed{tenantID: tenantID, branchID: uuid.New(), tableID: uuid.New()}

	qrSvc := service.NewQRService(service.QRParams{
		DB: sharedPool, QRRepo: repo.NewQRCodeRepo(), Logger: zap.NewNop(),
	})

	// A branch-scoped principal whose own BranchID matches satisfies
	// requireBranch's direct-match arm, so no OPA scope needs planting: the
	// fixture stays independent of the policy bundle.
	principal := auth.Principal{
		Ctx: auth.ContextStaff, TenantID: tenantID, BranchID: seed.branchID, PersonID: uuid.New(),
	}

	issued, err := qrSvc.Issue(context.Background(), tenantID, principal, service.IssueRequest{
		BranchID:   seed.branchID,
		TableID:    seed.tableID,
		TableLabel: "Masa 1",
		CreatedBy:  principal.PersonID,
	})
	require.NoError(t, err)
	seed.rawToken = issued.RawToken
	return seed
}

func newSessionService(t *testing.T, tables pospub.GuestTableReader) *service.SessionService {
	t.Helper()
	signer, err := auth.NewGuestTokenSigner([]byte("session-test-secret-32-bytes!!!!"))
	require.NoError(t, err)
	return service.NewSessionService(service.SessionParams{
		DB:     sharedPool,
		QRRepo: repo.NewQRCodeRepo(),
		Tables: tables,
		Signer: signer,
		Logger: zap.NewNop(),
	})
}

func TestResolveToken_ValidCode_MintsSessionForItsOwnTenant(t *testing.T) {
	seed := seedQRCode(t, uuid.New())
	svc := newSessionService(t, stubTables{table: pospub.GuestTable{
		BranchID: seed.branchID, Label: "Masa 1 (yeni ad)", IsActive: true,
	}})

	resolved, err := svc.ResolveToken(context.Background(), seed.rawToken)
	require.NoError(t, err)

	assert.Equal(t, seed.tenantID, resolved.TenantID, "the tenant is derived from the token, never from the caller")
	assert.Equal(t, seed.branchID, resolved.BranchID)
	assert.Equal(t, seed.tableID, resolved.TableID)
	assert.NotEqual(t, uuid.Nil, resolved.SessionID)
	assert.NotEmpty(t, resolved.Token)
	assert.Equal(t, "Masa 1 (yeni ad)", resolved.TableLabel,
		"the live table name wins over the label frozen into the sticker")
}

func TestResolveToken_UnknownToken_IsNotFound(t *testing.T) {
	seedQRCode(t, uuid.New()) // ensure the table is not empty
	svc := newSessionService(t, stubTables{table: pospub.GuestTable{IsActive: true}})

	_, err := svc.ResolveToken(context.Background(), "definitely-not-a-real-token")
	require.ErrorIs(t, err, pub.ErrQRNotFound)
}

func TestResolveToken_EmptyToken_IsNotFound(t *testing.T) {
	svc := newSessionService(t, stubTables{table: pospub.GuestTable{IsActive: true}})

	_, err := svc.ResolveToken(context.Background(), "")
	require.ErrorIs(t, err, pub.ErrQRNotFound)
}

// TestResolveToken_RevokedCode_IsRevokedButStillA404 keeps the audit signal
// (the service can log "a retired sticker was scanned") without giving the
// caller an oracle: the HTTP layer maps both sentinels to 404.
func TestResolveToken_RevokedCode_IsRevokedButStillA404(t *testing.T) {
	seed := seedQRCode(t, uuid.New())

	require.NoError(t, sharedPool.WithTenantTx(context.Background(), seed.tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			`UPDATE storefront_qr_codes SET status = 'revoked' WHERE table_id = $1`, seed.tableID)
		return err
	}))

	svc := newSessionService(t, stubTables{table: pospub.GuestTable{BranchID: seed.branchID, IsActive: true}})
	_, err := svc.ResolveToken(context.Background(), seed.rawToken)

	require.ErrorIs(t, err, pub.ErrQRRevoked)
	assert.False(t, errors.Is(err, pub.ErrQRNotFound),
		"the two stay distinguishable in the service so a revoked scan can be logged")
}

// TestResolveToken_TableMovedToAnotherBranch_IsNotFound: a sticker that
// outlived its table's branch must mint nothing — otherwise the diner browses
// happily and only hits the wall at checkout.
func TestResolveToken_TableMovedToAnotherBranch_IsNotFound(t *testing.T) {
	seed := seedQRCode(t, uuid.New())
	svc := newSessionService(t, stubTables{table: pospub.GuestTable{
		BranchID: uuid.New(), IsActive: true, // moved
	}})

	_, err := svc.ResolveToken(context.Background(), seed.rawToken)
	require.ErrorIs(t, err, pub.ErrQRNotFound)
}

func TestResolveToken_DeactivatedTable_IsNotFound(t *testing.T) {
	seed := seedQRCode(t, uuid.New())
	svc := newSessionService(t, stubTables{table: pospub.GuestTable{
		BranchID: seed.branchID, IsActive: false,
	}})

	_, err := svc.ResolveToken(context.Background(), seed.rawToken)
	require.ErrorIs(t, err, pub.ErrQRNotFound)
}

func TestResolveToken_DeletedTable_IsNotFound(t *testing.T) {
	seed := seedQRCode(t, uuid.New())
	svc := newSessionService(t, stubTables{err: pospub.ErrTableNotFound})

	_, err := svc.ResolveToken(context.Background(), seed.rawToken)
	require.ErrorIs(t, err, pub.ErrQRNotFound)
}

// TestResolveToken_OtherTenantsTokenResolvesToItsOwnTenant is the cross-tenant
// invariant of the bootstrap read: the lookup runs with NO tenant context, so
// the row it finds must be the token's own — never one from the "current"
// tenant, of which there is none.
func TestResolveToken_OtherTenantsTokenResolvesToItsOwnTenant(t *testing.T) {
	tenantA, tenantB := uuid.New(), uuid.New()
	seedA := seedQRCode(t, tenantA)
	seedB := seedQRCode(t, tenantB)

	svcA := newSessionService(t, stubTables{table: pospub.GuestTable{BranchID: seedA.branchID, IsActive: true}})
	resolvedA, err := svcA.ResolveToken(context.Background(), seedA.rawToken)
	require.NoError(t, err)
	assert.Equal(t, tenantA, resolvedA.TenantID)

	svcB := newSessionService(t, stubTables{table: pospub.GuestTable{BranchID: seedB.branchID, IsActive: true}})
	resolvedB, err := svcB.ResolveToken(context.Background(), seedB.rawToken)
	require.NoError(t, err)
	assert.Equal(t, tenantB, resolvedB.TenantID)
	assert.NotEqual(t, resolvedA.SessionID, resolvedB.SessionID)
}

// TestResolveToken_SessionIDIsFreshPerScan: two scans of the SAME sticker are
// two diners. Sharing a session id would let one read the other's orders.
func TestResolveToken_SessionIDIsFreshPerScan(t *testing.T) {
	seed := seedQRCode(t, uuid.New())
	svc := newSessionService(t, stubTables{table: pospub.GuestTable{BranchID: seed.branchID, IsActive: true}})

	first, err := svc.ResolveToken(context.Background(), seed.rawToken)
	require.NoError(t, err)
	second, err := svc.ResolveToken(context.Background(), seed.rawToken)
	require.NoError(t, err)

	assert.NotEqual(t, first.SessionID, second.SessionID)
	assert.NotEqual(t, first.Token, second.Token)
}
