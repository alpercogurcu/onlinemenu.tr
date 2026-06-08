package repo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// InsertOutbox records a domain event in pos_outbox within the caller's transaction.
// Must be called inside WithTenantTx so the insert shares the tenant RLS context.
func InsertOutbox(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, aggregateType, aggregateID, eventType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("pos/repo: marshal outbox payload: %w", err)
	}
	// Pass as string so pgx sends it as text; Postgres coerces text → JSONB.
	_, err = tx.Exec(ctx, `
		INSERT INTO pos_outbox (event_id, tenant_id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, uuid.New(), tenantID, aggregateType, aggregateID, eventType, string(data))
	if err != nil {
		return fmt.Errorf("pos/repo: insert outbox: %w", err)
	}
	return nil
}
