package repo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// InsertOutbox appends an immutable catalog domain event to catalog_outbox
// (ADR-DATA-001, ADR-DATA-002).
//
// eventType carries NO module prefix: the dispatcher builds the NATS subject
// as "<module>.<eventType>.v1", so "branch_override.changed" becomes
// "catalog.branch_override.changed.v1". Passing "catalog.branch_override.
// changed" here would publish it twice-prefixed.
func InsertOutbox(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, aggregateType, aggregateID, eventType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("catalog/repo: marshal outbox payload: %w", err)
	}
	// Passed as string so pgx sends text; Postgres coerces text → JSONB.
	_, err = tx.Exec(ctx, `
		INSERT INTO catalog_outbox (event_id, tenant_id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, uuid.New(), tenantID, aggregateType, aggregateID, eventType, string(data))
	if err != nil {
		return fmt.Errorf("catalog/repo: insert outbox: %w", err)
	}
	return nil
}
