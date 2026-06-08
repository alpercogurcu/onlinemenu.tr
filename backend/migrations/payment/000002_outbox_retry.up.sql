-- payment_outbox: retry / dead-letter kolonları (ADR-DATA-001)
ALTER TABLE payment_outbox
    ADD COLUMN IF NOT EXISTS retry_count   INT         NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_error    TEXT,
    ADD COLUMN IF NOT EXISTS is_dead       BOOLEAN     NOT NULL DEFAULT FALSE;

DROP INDEX IF EXISTS payment_outbox_unprocessed_idx;
CREATE INDEX payment_outbox_dispatchable_idx ON payment_outbox (created_at)
    WHERE processed_at IS NULL AND is_dead = FALSE;
