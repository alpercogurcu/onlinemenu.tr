-- pos_outbox: retry / dead-letter kolonları (ADR-DATA-001)
ALTER TABLE pos_outbox
    ADD COLUMN IF NOT EXISTS retry_count   INT         NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_error    TEXT,
    ADD COLUMN IF NOT EXISTS is_dead       BOOLEAN     NOT NULL DEFAULT FALSE;

-- Daha seçici index: dead event'leri ve retry zamanı gelmemiş olanları atlar.
DROP INDEX IF EXISTS pos_outbox_unprocessed_idx;
CREATE INDEX pos_outbox_dispatchable_idx ON pos_outbox (created_at)
    WHERE processed_at IS NULL AND is_dead = FALSE;
