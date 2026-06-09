package repo_test

import (
	"context"
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
	"go.uber.org/goleak"

	"onlinemenu.tr/internal/modules/party/domain"
	"onlinemenu.tr/internal/modules/party/repo"
	"onlinemenu.tr/internal/platform/db"
)

var (
	sharedPool *db.Pool
	tenantA    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
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

func migrationsBase() string {
	_, file, _, _ := runtime.Caller(0)
	// file = .../backend/internal/modules/party/repo/integration_test.go
	// filepath.Dir(file) → repo/
	// +4 → backend/
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

	for _, mod := range []string{"tenant", "party"} {
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

func withTx(ctx context.Context, t *testing.T, tenantID uuid.UUID, f func(pgx.Tx)) {
	t.Helper()
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		f(tx)
		return nil
	})
	require.NoError(t, err)
}

func sampleParty(name string) domain.Party {
	return domain.Party{
		TenantID:          tenantA,
		PartyType:         domain.PartyTypeSupplier,
		Name:              name,
		ShortName:         "TEST",
		TaxNo:             "1234567890",
		TaxOffice:         "İstanbul",
		GibAlias:          "urn:mail:test@edm.com.tr",
		Phone:             "+905001234567",
		Email:             "test@example.com",
		Website:           "https://example.com",
		AddressLine:       "Örnek Sokak No:1",
		City:              "İstanbul",
		District:          "Kadıköy",
		PostalCode:        "34710",
		PaymentTermsDays:  30,
		CreditLimitAmount: 1000000,
		Currency:          "TRY",
		IsActive:          true,
		Notes:             "Test party",
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestPartyRepo_Create(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPartyRepo()

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, sampleParty("Akın Gıda A.Ş."))
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, created.ID)
		assert.Equal(t, domain.PartyTypeSupplier, created.PartyType)
		assert.Equal(t, "Akın Gıda A.Ş.", created.Name)
		assert.Equal(t, "TRY", created.Currency)
		assert.True(t, created.IsActive)
	})
}

func TestPartyRepo_GetByID(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPartyRepo()

	var createdID uuid.UUID
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, sampleParty("Bozkurt Et A.Ş."))
		require.NoError(t, err)
		createdID = created.ID
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		fetched, err := r.GetByID(ctx, tx, createdID)
		require.NoError(t, err)
		assert.Equal(t, createdID, fetched.ID)
		assert.Equal(t, "Bozkurt Et A.Ş.", fetched.Name)
		assert.Equal(t, "İstanbul", fetched.City)
		assert.Equal(t, int64(1000000), fetched.CreditLimitAmount)
	})
}

func TestPartyRepo_GetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPartyRepo()

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		_, err := r.GetByID(ctx, tx, uuid.New())
		require.ErrorIs(t, err, repo.ErrNotFound)
	})
}

func TestPartyRepo_Update(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPartyRepo()

	var createdID uuid.UUID
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, sampleParty("Çelik Tavuk Ltd."))
		require.NoError(t, err)
		createdID = created.ID
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		updated, err := r.Update(ctx, tx, domain.Party{
			ID:                createdID,
			TenantID:          tenantA,
			PartyType:         domain.PartyTypeBoth,
			Name:              "Çelik Tavuk & Kasap Ltd.",
			TaxNo:             "1234567890",
			PaymentTermsDays:  45,
			CreditLimitAmount: 2000000,
			Currency:          "TRY",
			IsActive:          true,
		})
		require.NoError(t, err)
		assert.Equal(t, domain.PartyTypeBoth, updated.PartyType)
		assert.Equal(t, "Çelik Tavuk & Kasap Ltd.", updated.Name)
		assert.Equal(t, 45, updated.PaymentTermsDays)
	})
}

func TestPartyRepo_List(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPartyRepo()

	// Create parties of different types.
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		for i := range 2 {
			p := sampleParty(fmt.Sprintf("Tedarikçi %d", i))
			p.PartyType = domain.PartyTypeSupplier
			_, err := r.Create(ctx, tx, p)
			require.NoError(t, err)
		}
		cust := sampleParty("Müşteri Firma")
		cust.PartyType = domain.PartyTypeCustomer
		_, err := r.Create(ctx, tx, cust)
		require.NoError(t, err)
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		all, err := r.List(ctx, tx, tenantA, "", 100, 0)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(all), 3)
	})

	// Filter by supplier type — should include "both" parties too.
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		suppliers, err := r.List(ctx, tx, tenantA, domain.PartyTypeSupplier, 100, 0)
		require.NoError(t, err)
		for _, p := range suppliers {
			assert.True(t, p.PartyType == domain.PartyTypeSupplier || p.PartyType == domain.PartyTypeBoth)
		}
	})
}

func TestPartyRepo_SearchByName(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPartyRepo()

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		_, err := r.Create(ctx, tx, sampleParty("Altın Un Fabrikası"))
		require.NoError(t, err)
		_, err = r.Create(ctx, tx, sampleParty("Altın Market Zinciri"))
		require.NoError(t, err)
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		results, err := r.SearchByName(ctx, tx, tenantA, "altın", 10)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(results), 2)
		for _, p := range results {
			assert.Contains(t, p.Name, "Altın")
		}
	})
}

func TestPartyRepo_AddAndListContacts(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPartyRepo()

	var partyID uuid.UUID
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, sampleParty("Doğan Lojistik A.Ş."))
		require.NoError(t, err)
		partyID = created.ID
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		c, err := r.AddContact(ctx, tx, domain.Contact{
			TenantID:  tenantA,
			PartyID:   partyID,
			Name:      "Ahmet Yılmaz",
			Role:      "Satın Alma Müdürü",
			Phone:     "+905551234567",
			Email:     "ahmet@dogan.com",
			IsPrimary: true,
		})
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, c.ID)
		assert.Equal(t, "Ahmet Yılmaz", c.Name)
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		party, err := r.GetByID(ctx, tx, partyID)
		require.NoError(t, err)
		require.Len(t, party.Contacts, 1)
		assert.Equal(t, "Ahmet Yılmaz", party.Contacts[0].Name)
		assert.True(t, party.Contacts[0].IsPrimary)
	})
}

func TestPartyRepo_DeleteContact(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPartyRepo()

	var partyID, contactID uuid.UUID
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, sampleParty("Ege Deniz Ürünleri Ltd."))
		require.NoError(t, err)
		partyID = created.ID

		c, err := r.AddContact(ctx, tx, domain.Contact{
			TenantID: tenantA,
			PartyID:  partyID,
			Name:     "Zeynep Kaya",
			Role:     "Muhasebe",
		})
		require.NoError(t, err)
		contactID = c.ID
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		err := r.DeleteContact(ctx, tx, contactID)
		require.NoError(t, err)
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		party, err := r.GetByID(ctx, tx, partyID)
		require.NoError(t, err)
		assert.Empty(t, party.Contacts)
	})
}

func TestPartyRepo_RLSIsolation(t *testing.T) {
	ctx := context.Background()
	r := repo.NewPartyRepo()

	tenantB := uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002")

	var createdID uuid.UUID
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, sampleParty("Gizli Tedarikçi"))
		require.NoError(t, err)
		createdID = created.ID
	})

	// tenantB must not read tenantA's party.
	err := sharedPool.WithTenantReadTx(ctx, tenantB, func(tx pgx.Tx) error {
		_, err := r.GetByID(ctx, tx, createdID)
		return err
	})
	require.ErrorIs(t, err, repo.ErrNotFound)
}
