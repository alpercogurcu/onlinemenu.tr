package outbox_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"onlinemenu.tr/internal/modules/edge-sync/domain"
	"onlinemenu.tr/internal/modules/edge-sync/outbox"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_journal_mode=WAL&_foreign_keys=on")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS outbox_events (
			id             TEXT    PRIMARY KEY,
			event_type     TEXT    NOT NULL,
			aggregate_type TEXT    NOT NULL,
			aggregate_id   TEXT    NOT NULL,
			payload        TEXT    NOT NULL,
			created_at     INTEGER NOT NULL DEFAULT (unixepoch()),
			published_at   INTEGER,
			retry_count    INTEGER NOT NULL DEFAULT 0,
			last_error     TEXT    NOT NULL DEFAULT '',
			is_dead        INTEGER NOT NULL DEFAULT 0
		)`)
	require.NoError(t, err)
	return db
}

func TestOutboxWriter_Write(t *testing.T) {
	db := openTestDB(t)
	w := outbox.NewWriter(db)

	ev := domain.OutboxEvent{
		ID:            "evt-001",
		EventType:     "order.created.v1",
		AggregateType: "order",
		AggregateID:   "order-abc",
		Payload:       `{"table_id":"t1"}`,
		CreatedAt:     time.Now().UTC(),
	}

	require.NoError(t, w.Write(context.Background(), ev))

	n, err := w.PendingCount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

func TestOutboxWriter_Write_Idempotent(t *testing.T) {
	db := openTestDB(t)
	w := outbox.NewWriter(db)

	ev := domain.OutboxEvent{
		ID:            "evt-idempotent",
		EventType:     "order.created.v1",
		AggregateType: "order",
		AggregateID:   "order-xyz",
		Payload:       `{}`,
	}

	require.NoError(t, w.Write(context.Background(), ev))
	require.NoError(t, w.Write(context.Background(), ev)) // second write must not error or duplicate

	n, err := w.PendingCount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "duplicate write must not increase count")
}

func TestOutboxWriter_Write_AutoID(t *testing.T) {
	db := openTestDB(t)
	w := outbox.NewWriter(db)

	ev := domain.OutboxEvent{
		// ID intentionally empty — writer must generate one
		EventType:     "payment.completed.v1",
		AggregateType: "payment",
		AggregateID:   "pay-001",
		Payload:       `{"amount":100}`,
	}
	require.NoError(t, w.Write(context.Background(), ev))

	n, err := w.PendingCount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

func TestOutboxWriter_PendingCount_IgnoresDead(t *testing.T) {
	db := openTestDB(t)
	w := outbox.NewWriter(db)

	// Insert a live event and a dead event directly.
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO outbox_events (id, event_type, aggregate_type, aggregate_id, payload, is_dead)
		VALUES ('live-1', 'x', 'x', 'x', '{}', 0),
		       ('dead-1', 'x', 'x', 'x', '{}', 1)`)
	require.NoError(t, err)

	n, err := w.PendingCount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "dead events must not be counted as pending")
}
