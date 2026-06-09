package edgedb_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/platform/edgedb"
)

func TestOpen_InMemory(t *testing.T) {
	db, err := edgedb.Open(":memory:")
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, db.Migrate(context.Background()))

	// Verify all expected tables exist.
	tables := []string{
		"branch_config",
		"catalog_snapshot",
		"outbox_events",
		"inbox_events",
	}
	for _, tbl := range tables {
		var name string
		err := db.SQL().QueryRowContext(context.Background(),
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl,
		).Scan(&name)
		require.NoError(t, err, "table %s must exist", tbl)
		assert.Equal(t, tbl, name)
	}
}

func TestOpen_FilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	db, err := edgedb.Open(path)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(context.Background()))
	db.Close()

	// File must exist on disk after close.
	_, err = os.Stat(path)
	require.NoError(t, err, "db file must exist on disk")
}

func TestMigrate_Idempotent(t *testing.T) {
	db, err := edgedb.Open(":memory:")
	require.NoError(t, err)
	defer db.Close()

	// Running Migrate twice must not fail (all statements use IF NOT EXISTS).
	require.NoError(t, db.Migrate(context.Background()))
	require.NoError(t, db.Migrate(context.Background()))
}
