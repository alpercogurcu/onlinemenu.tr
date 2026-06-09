// Package outbox provides the local SQLite outbox writer for the edge server.
// Events written here are flushed to cloud NATS by the sync engine (Phase 2).
package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/edge-sync/domain"
)

// Writer inserts domain events into the local outbox table.
type Writer struct {
	db *sql.DB
}

// NewWriter constructs an outbox Writer.
func NewWriter(db *sql.DB) *Writer {
	return &Writer{db: db}
}

// Write inserts ev into the outbox. Idempotent: a duplicate ID is silently ignored.
func (w *Writer) Write(ctx context.Context, ev domain.OutboxEvent) error {
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now().UTC()
	}
	_, err := w.db.ExecContext(ctx, `
		INSERT INTO outbox_events (id, event_type, aggregate_type, aggregate_id, payload, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO NOTHING`,
		ev.ID, ev.EventType, ev.AggregateType, ev.AggregateID,
		ev.Payload, ev.CreatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("outbox: write: %w", err)
	}
	return nil
}

// PendingCount returns the number of events not yet published to cloud.
func (w *Writer) PendingCount(ctx context.Context) (int, error) {
	var n int
	err := w.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE published_at IS NULL AND is_dead = 0`,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("outbox: pending count: %w", err)
	}
	return n, nil
}
