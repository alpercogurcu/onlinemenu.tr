package repo_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.uber.org/goleak"

	"onlinemenu.tr/internal/modules/billing/domain"
	"onlinemenu.tr/internal/modules/billing/repo"
	"onlinemenu.tr/internal/platform/db"
)

var (
	sharedPool *db.Pool
	tenantA    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	branchA    = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")
)

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
		fmt.Fprintf(os.Stderr, "run migrations: %v\n", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}

	sharedPool = newPool(ctx, superDSN)
	rc := m.Run()
	sharedPool.Close()
	_ = ctr.Terminate(ctx)

	if err := goleak.Find(
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*DockerContainer).followOutput"),
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*DockerContainer).tailOrFollowOutput"),
	); err != nil {
		fmt.Fprintf(os.Stderr, "goleak: %v\n", err)
		rc = 1
	}
	os.Exit(rc)
}

// migrationsBase returns the absolute path to backend/migrations.
func migrationsBase() string {
	_, file, _, _ := runtime.Caller(0)
	// file = .../backend/internal/modules/billing/repo/integration_test.go
	// filepath.Dir(file) → repo/
	// +1 → billing/, +2 → modules/, +3 → internal/, +4 → backend/
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
	migratorDSN := fmt.Sprintf("pgx5://app_migrator:migrator_secret@%s/%s?sslmode=disable",
		fmt.Sprintf("%s:%d", cfg.ConnConfig.Host, cfg.ConnConfig.Port),
		cfg.ConnConfig.Database,
	)

	for _, mod := range []string{"tenant", "billing"} {
		absPath := filepath.Join(migrationsBase(), mod)
		src := fmt.Sprintf("file://%s", absPath)
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
			return fmt.Errorf("stmt: %w", err)
		}
	}
	return nil
}

func newPool(ctx context.Context, superDSN string) *db.Pool {
	cfg, err := pgxpool.ParseConfig(superDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse pool config: %v\n", err)
		os.Exit(1)
	}
	cfg.ConnConfig.User = "app_runtime"
	cfg.ConnConfig.Password = "runtime_secret"
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.MaxConns = 5

	p, err := db.NewPoolFromConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create pool: %v\n", err)
		os.Exit(1)
	}
	return p
}

// withTx opens a tenant-scoped transaction and calls f.
func withTx(ctx context.Context, t *testing.T, tenantID uuid.UUID, f func(pgx.Tx)) {
	t.Helper()
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		f(tx)
		return nil
	})
	require.NoError(t, err)
}

// sampleInvoice returns a fully populated Invoice for testing.
func sampleInvoice(key string) domain.Invoice {
	prod := uuid.New()
	return domain.Invoice{
		TenantID:       tenantA,
		BranchID:       branchA,
		InvoiceType:    domain.InvoiceTypeEArsiv,
		IdempotencyKey: key,
		GibUUID:        uuid.New(),
		SupplierVKN:    "1234567890",
		SupplierName:   "TEST TEDARİKÇİ A.Ş.",
		SupplierAlias:  "urn:mail:test@edm.com.tr",
		CustomerVKN:    "9876543210",
		CustomerName:   "ALICI FİRMA LTD.",
		IssueDate:      time.Now().UTC(),
		Currency:       "TRY",
		Items: []domain.InvoiceItem{
			{
				ProductID:       &prod,
				ProductName:     "Adana Kebap",
				Quantity:        2,
				UnitPriceAmount: 15000, // 150.00 TRY
				TaxRateBPS:      800,   // 8%
				LineTotal:       30000, // 300.00 TRY
				TaxAmount:       2400,  // 24.00 TRY
			},
		},
		AmountExcludingTax: 30000,
		TaxAmount:          2400,
		AmountTotal:        32400,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestInvoiceRepo_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	r := repo.NewInvoiceRepo()

	inv := sampleInvoice("test-create-001")

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, inv)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, created.ID)
		assert.Equal(t, domain.InvoiceStatusDraft, created.Status)
		assert.Equal(t, "test-create-001", created.IdempotencyKey)
		assert.Len(t, created.Items, 1)
		assert.Equal(t, int64(30000), created.Items[0].LineTotal)
	})
}

func TestInvoiceRepo_GetByID(t *testing.T) {
	ctx := context.Background()
	r := repo.NewInvoiceRepo()

	var createdID uuid.UUID
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, sampleInvoice("test-get-by-id-001"))
		require.NoError(t, err)
		createdID = created.ID
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		fetched, err := r.GetByID(ctx, tx, createdID)
		require.NoError(t, err)
		assert.Equal(t, createdID, fetched.ID)
		assert.Equal(t, "TEST TEDARİKÇİ A.Ş.", fetched.SupplierName)
		assert.Len(t, fetched.Items, 1)
	})
}

func TestInvoiceRepo_GetByIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	r := repo.NewInvoiceRepo()

	key := "test-idem-001"
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		_, err := r.Create(ctx, tx, sampleInvoice(key))
		require.NoError(t, err)
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		fetched, err := r.GetByIdempotencyKey(ctx, tx, tenantA, key)
		require.NoError(t, err)
		assert.Equal(t, key, fetched.IdempotencyKey)
	})
}

func TestInvoiceRepo_DuplicateIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	r := repo.NewInvoiceRepo()

	key := "test-dup-001"
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		_, err := r.Create(ctx, tx, sampleInvoice(key))
		require.NoError(t, err)
	})

	// Second attempt with same key must fail with ErrDuplicateIdempotencyKey.
	// The transaction is aborted by PostgreSQL on the unique violation, so we
	// propagate the error out of WithTenantTx and check it directly.
	var createErr error
	_ = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, createErr = r.Create(ctx, tx, sampleInvoice(key))
		return createErr
	})
	require.ErrorIs(t, createErr, repo.ErrDuplicateIdempotencyKey)
}

func TestInvoiceRepo_UpdateStatus(t *testing.T) {
	ctx := context.Background()
	r := repo.NewInvoiceRepo()

	var createdID uuid.UUID
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, sampleInvoice("test-update-status-001"))
		require.NoError(t, err)
		createdID = created.ID
	})

	now := time.Now().UTC()
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		err := r.UpdateStatus(ctx, tx,
			createdID,
			domain.InvoiceStatusSubmitted,
			"INTL-TXN-12345",
			&now, nil, nil, "",
		)
		require.NoError(t, err)
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		fetched, err := r.GetByID(ctx, tx, createdID)
		require.NoError(t, err)
		assert.Equal(t, domain.InvoiceStatusSubmitted, fetched.Status)
		assert.Equal(t, "INTL-TXN-12345", fetched.ExternalID)
		assert.NotNil(t, fetched.SubmittedAt)
	})
}

func TestInvoiceRepo_List(t *testing.T) {
	ctx := context.Background()
	r := repo.NewInvoiceRepo()

	for i := range 3 {
		withTx(ctx, t, tenantA, func(tx pgx.Tx) {
			_, err := r.Create(ctx, tx, sampleInvoice(fmt.Sprintf("test-list-%03d", i)))
			require.NoError(t, err)
		})
	}

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		invoices, err := r.List(ctx, tx, tenantA, 10, 0)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(invoices), 3)
	})
}

func TestInvoiceRepo_NextInvoiceSequence(t *testing.T) {
	ctx := context.Background()
	r := repo.NewInvoiceRepo()

	year := time.Now().Year()
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		seq1, err := r.NextInvoiceSequence(ctx, tx, tenantA, year)
		require.NoError(t, err)
		assert.Greater(t, seq1, 0)

		// After creating one more the sequence should increment.
		inv := sampleInvoice(fmt.Sprintf("test-seq-%d", seq1))
		_, err = r.Create(ctx, tx, inv)
		require.NoError(t, err)

		seq2, err := r.NextInvoiceSequence(ctx, tx, tenantA, year)
		require.NoError(t, err)
		assert.Equal(t, seq1+1, seq2)
	})
}

func TestInvoiceRepo_RLSIsolation(t *testing.T) {
	ctx := context.Background()
	r := repo.NewInvoiceRepo()

	tenantB := uuid.MustParse("cccccccc-0000-0000-0000-000000000002")
	key := "test-rls-isolation-001"

	// Create invoice as tenantA.
	var createdID uuid.UUID
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, sampleInvoice(key))
		require.NoError(t, err)
		createdID = created.ID
	})

	// tenantB must not be able to read tenantA's invoice.
	err := sharedPool.WithTenantReadTx(ctx, tenantB, func(tx pgx.Tx) error {
		_, err := r.GetByID(ctx, tx, createdID)
		return err
	})
	require.ErrorIs(t, err, repo.ErrNotFound)
}
