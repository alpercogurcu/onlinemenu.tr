package edgedb

// schema is the edge SQLite schema. All CREATE statements use IF NOT EXISTS so
// Migrate() is idempotent and safe to call on every binary startup.
const schema = `
-- Branch configuration pushed down from cloud (read-only on edge).
CREATE TABLE IF NOT EXISTS branch_config (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

-- Catalog snapshot: products, categories, modifiers pushed by cloud.
-- Keyed by (resource_type, resource_id) so each resource type is independently versioned.
CREATE TABLE IF NOT EXISTS catalog_snapshot (
    resource_type TEXT    NOT NULL,
    resource_id   TEXT    NOT NULL,
    payload       TEXT    NOT NULL,
    version       INTEGER NOT NULL DEFAULT 0,
    updated_at    INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (resource_type, resource_id)
);

-- Events generated locally (orders, payments) waiting to be published to cloud NATS.
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
);

-- Fast lookup for the outbox flush worker: only unpublished, live events.
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
    ON outbox_events (created_at)
    WHERE published_at IS NULL AND is_dead = 0;

-- Events delivered from cloud NATS (catalog deltas, config updates) pending local apply.
CREATE TABLE IF NOT EXISTS inbox_events (
    id          TEXT    PRIMARY KEY,
    event_type  TEXT    NOT NULL,
    payload     TEXT    NOT NULL,
    received_at INTEGER NOT NULL DEFAULT (unixepoch()),
    applied_at  INTEGER
);

-- Fast lookup for the inbox apply worker: only unapplied events.
CREATE INDEX IF NOT EXISTS idx_inbox_unapplied
    ON inbox_events (received_at)
    WHERE applied_at IS NULL;
`
