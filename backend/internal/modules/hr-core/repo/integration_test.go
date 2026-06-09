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

	"onlinemenu.tr/internal/modules/hr-core/domain"
	"onlinemenu.tr/internal/modules/hr-core/repo"
	"onlinemenu.tr/internal/platform/db"
)

var (
	sharedPool *db.Pool
	superDSN   string
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

	superDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
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
	// file = .../backend/internal/modules/hr-core/repo/integration_test.go
	// filepath.Dir(file) → repo/
	// +4 → backend/
	base := filepath.Dir(file)
	for range 4 {
		base = filepath.Dir(base)
	}
	return filepath.Join(base, "migrations")
}

func runMigrations(dsn string) error {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}
	migratorDSN := fmt.Sprintf("pgx5://app_migrator:migrator_secret@%s/%s?sslmode=disable",
		fmt.Sprintf("%s:%d", cfg.ConnConfig.Host, cfg.ConnConfig.Port),
		cfg.ConnConfig.Database,
	)

	// tenant must run before identity (roles references tenants/branches).
	// identity must run before hr-core (employee_profiles references persons).
	for _, mod := range []string{"tenant", "identity", "hr-core"} {
		absPath := filepath.Join(migrationsBase(), mod)
		src := fmt.Sprintf("file://%s", absPath)
		tableName := "schema_migrations_" + mod
		if mod == "hr-core" {
			tableName = "schema_migrations_hr_core"
		}
		migDSN := fmt.Sprintf("%s&x-migrations-table=%s", migratorDSN, tableName)

		mg, err := migrate.New(src, migDSN)
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
			return fmt.Errorf("stmt: %w", err)
		}
	}
	return nil
}

func newPool(ctx context.Context, dsn string) *db.Pool {
	cfg, err := pgxpool.ParseConfig(dsn)
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

// seedPerson inserts a person using the superuser connection (bypasses RLS).
// Returns the person's ID for use as employee_profiles.person_id FK.
func seedPerson(t *testing.T, email, fullName string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, superDSN)
	require.NoError(t, err)
	defer conn.Close(ctx)

	id := uuid.New()
	_, err = conn.Exec(ctx,
		`INSERT INTO persons (id, keycloak_sub, email, full_name) VALUES ($1, $2, $3, $4)`,
		id, "test-sub-"+id.String(), email, fullName,
	)
	require.NoError(t, err)
	return id
}

func withTx(ctx context.Context, t *testing.T, tenantID uuid.UUID, f func(pgx.Tx)) {
	t.Helper()
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		f(tx)
		return nil
	})
	require.NoError(t, err)
}

func sampleEmployee(personID uuid.UUID) domain.Employee {
	hireDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	return domain.Employee{
		PersonID:       personID,
		TenantID:       tenantA,
		Department:     "Mutfak",
		JobTitle:       "Aşçı",
		EmploymentType: domain.EmploymentTypeFull,
		TCKimlikHash:   "sha256:abc123",
		HireDate:       hireDate,
		ContactInfo: domain.ContactInfo{
			Phone:   "+905001234567",
			Address: "İstanbul, Kadıköy",
		},
		EmergencyContact: domain.EmergencyContact{
			Name:     "Ayşe Yılmaz",
			Phone:    "+905551234567",
			Relation: "Eş",
		},
		Notes: "Test personel",
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestEmployeeRepo_Create(t *testing.T) {
	ctx := context.Background()
	r := repo.NewEmployeeRepo()
	personID := seedPerson(t, "mehmet@test.com", "Mehmet Demir")

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, sampleEmployee(personID))
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, created.ID)
		assert.Equal(t, domain.EmployeeStatusActive, created.Status)
		assert.Equal(t, domain.EmploymentTypeFull, created.EmploymentType)
		assert.Equal(t, "Mutfak", created.Department)
		assert.Equal(t, "+905001234567", created.ContactInfo.Phone)
		assert.Equal(t, "Ayşe Yılmaz", created.EmergencyContact.Name)
	})
}

func TestEmployeeRepo_GetByID(t *testing.T) {
	ctx := context.Background()
	r := repo.NewEmployeeRepo()
	personID := seedPerson(t, "ali@test.com", "Ali Çelik")

	var createdID uuid.UUID
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, sampleEmployee(personID))
		require.NoError(t, err)
		createdID = created.ID
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		fetched, err := r.GetByID(ctx, tx, createdID)
		require.NoError(t, err)
		assert.Equal(t, createdID, fetched.ID)
		assert.Equal(t, personID, fetched.PersonID)
		assert.Equal(t, "Aşçı", fetched.JobTitle)
	})
}

func TestEmployeeRepo_GetByPersonID(t *testing.T) {
	ctx := context.Background()
	r := repo.NewEmployeeRepo()
	personID := seedPerson(t, "fatma@test.com", "Fatma Kaya")

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		_, err := r.Create(ctx, tx, sampleEmployee(personID))
		require.NoError(t, err)
	})

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		fetched, err := r.GetByPersonID(ctx, tx, tenantA, personID)
		require.NoError(t, err)
		assert.Equal(t, personID, fetched.PersonID)
	})
}

func TestEmployeeRepo_GetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	r := repo.NewEmployeeRepo()

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		_, err := r.GetByID(ctx, tx, uuid.New())
		require.ErrorIs(t, err, repo.ErrNotFound)
	})
}

func TestEmployeeRepo_DuplicateEmployee(t *testing.T) {
	ctx := context.Background()
	r := repo.NewEmployeeRepo()
	personID := seedPerson(t, "zeynep@test.com", "Zeynep Arslan")

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		_, err := r.Create(ctx, tx, sampleEmployee(personID))
		require.NoError(t, err)
	})

	// Second profile for same person+tenant must fail.
	var createErr error
	_ = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, createErr = r.Create(ctx, tx, sampleEmployee(personID))
		return createErr
	})
	require.ErrorIs(t, createErr, repo.ErrDuplicateEmployee)
}

func TestEmployeeRepo_Update(t *testing.T) {
	ctx := context.Background()
	r := repo.NewEmployeeRepo()
	personID := seedPerson(t, "hasan@test.com", "Hasan Koç")

	var createdID uuid.UUID
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, sampleEmployee(personID))
		require.NoError(t, err)
		createdID = created.ID
	})

	termDate := time.Date(2025, 6, 30, 0, 0, 0, 0, time.UTC)
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		e, err := r.GetByID(ctx, tx, createdID)
		require.NoError(t, err)
		e.Department = "Servis"
		e.JobTitle = "Garson"
		e.Status = domain.EmployeeStatusOnLeave
		e.TerminationDate = &termDate
		updated, err := r.Update(ctx, tx, e)
		require.NoError(t, err)
		assert.Equal(t, "Servis", updated.Department)
		assert.Equal(t, domain.EmployeeStatusOnLeave, updated.Status)
		assert.NotNil(t, updated.TerminationDate)
	})
}

func TestEmployeeRepo_List(t *testing.T) {
	ctx := context.Background()
	r := repo.NewEmployeeRepo()

	for i := range 3 {
		pid := seedPerson(t, fmt.Sprintf("personel%d@test.com", i+100), fmt.Sprintf("Personel %d", i+100))
		withTx(ctx, t, tenantA, func(tx pgx.Tx) {
			_, err := r.Create(ctx, tx, sampleEmployee(pid))
			require.NoError(t, err)
		})
	}

	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		all, err := r.List(ctx, tx, tenantA, "", 100, 0)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(all), 3)
	})

	// Filter by active status.
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		active, err := r.List(ctx, tx, tenantA, domain.EmployeeStatusActive, 100, 0)
		require.NoError(t, err)
		for _, e := range active {
			assert.Equal(t, domain.EmployeeStatusActive, e.Status)
		}
	})
}

func TestEmployeeRepo_RLSIsolation(t *testing.T) {
	ctx := context.Background()
	r := repo.NewEmployeeRepo()
	tenantB := uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002")
	personID := seedPerson(t, "rls.test@test.com", "RLS Test Personel")

	var createdID uuid.UUID
	withTx(ctx, t, tenantA, func(tx pgx.Tx) {
		created, err := r.Create(ctx, tx, sampleEmployee(personID))
		require.NoError(t, err)
		createdID = created.ID
	})

	// tenantB must not read tenantA's employee.
	err := sharedPool.WithTenantReadTx(ctx, tenantB, func(tx pgx.Tx) error {
		_, err := r.GetByID(ctx, tx, createdID)
		return err
	})
	require.ErrorIs(t, err, repo.ErrNotFound)
}
