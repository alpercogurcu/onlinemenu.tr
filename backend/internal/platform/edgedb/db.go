// Package edgedb provides a SQLite database handle for the edge (local server) binary.
// It runs in WAL mode for concurrent reads from POS tablets and uses database/sql
// with the modernc.org/sqlite pure-Go driver (no CGO, compatible with ko distroless).
package edgedb

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// DB wraps *sql.DB for the edge SQLite store.
type DB struct {
	inner *sql.DB
}

// Open creates or opens the SQLite database at path.
// WAL mode is enabled so POS tablet reads don't block writes.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("edgedb: open: %w", err)
	}
	// SQLite handles one writer at a time; a single connection avoids "database is locked".
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("edgedb: ping: %w", err)
	}
	return &DB{inner: db}, nil
}

// Migrate applies the embedded schema. Safe to call on every startup (IF NOT EXISTS).
func (d *DB) Migrate(ctx context.Context) error {
	if _, err := d.inner.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("edgedb: migrate: %w", err)
	}
	return nil
}

// SQL returns the underlying *sql.DB for packages that need direct access.
func (d *DB) SQL() *sql.DB { return d.inner }

// Close closes the database handle.
func (d *DB) Close() error { return d.inner.Close() }
