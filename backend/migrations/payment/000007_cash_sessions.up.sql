-- Migration: payment/000007_cash_sessions
-- ADR-DATA-008: kasa oturumu (cash session) reconciliation. Module ownership is
-- `payment`, not `pos` — see the ADR's "Modul sahipligi" section: the session's
-- content is entirely money (expected close = opening + cash payments taken +
-- movements) and its close guard needs fiscal-submission state, both of which
-- already live here.
--
-- Depends on: payment/000001_create_payment (payments table, used read-only by
-- the reconciliation queries added in the repo layer).

SET LOCAL role = app_migrator;

-- ─── cash_sessions ──────────────────────────────────────────────────────────
-- One row per shift/drawer reconciliation, branch-scoped (ADR-DATA-008 Karar 1
-- — terminal-scoped is the eventual target once ADR-SEC-004 ships station
-- identity; branch is the only viable key today since a branch has no station
-- concept yet).
--
-- Status machine (opening_control -> opened -> closing_control -> closed) is
-- enforced in Go (domain.Transition), never assigned ad hoc; this CHECK is a
-- second line of defence, not the source of truth. 'opening_control' is kept
-- in the enum for schema completeness and ADR symmetry with the two-step
-- closing flow, but no row is ever persisted in that status today: the "open"
-- endpoint is single-step (opening count is submitted at creation time), so a
-- session is written directly as 'opened'. This becomes meaningful the day
-- opening also splits into two steps (SEC-004 station identity landing is the
-- most likely trigger) — no migration is needed then, the value already exists.
--
-- Stored vs computed (the core of ADR-DATA-008's data model): opening_counted_
-- amount and closing_counted_amount are stored inputs a human counted. The
-- cash-movement "total" is stored as the individual cash_movements rows below
-- (the ledger), never collapsed into a running-total column here — a
-- denormalized counter would have to be kept in lockstep with every insert
-- into cash_movements and would drift the moment it wasn't. expected_close and
-- difference are NEVER stored anywhere (not as columns, not as rows): both are
-- computed at read time in the service layer from opening_counted_amount +
-- completed cash payments in the session window + cash_movements, exactly so
-- they cannot go stale when an input changes after the fact.
CREATE TABLE IF NOT EXISTS cash_sessions (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL,
    branch_id               UUID        NOT NULL,
    status                  TEXT        NOT NULL DEFAULT 'opening_control'
        CHECK (status IN ('opening_control', 'opened', 'closing_control', 'closed')),

    opening_counted_amount  BIGINT      NOT NULL CHECK (opening_counted_amount >= 0),
    opening_notes           TEXT        NOT NULL DEFAULT '',
    opened_by               UUID        NOT NULL,
    opened_at               TIMESTAMPTZ NOT NULL,

    -- Set only once closing_control is reached; NULL while opened.
    closing_counted_amount  BIGINT      CHECK (closing_counted_amount IS NULL OR closing_counted_amount >= 0),
    -- Kupur dokumu: [{"denomination_minor": 20000, "count": 3}, ...]. The
    -- Turkish denomination list is fixed (ADR-DATA-008) and therefore not a
    -- tenant-configurable table — it lives in the POS client, not here.
    closing_denominations   JSONB,
    closing_notes           TEXT        NOT NULL DEFAULT '',
    closing_submitted_at    TIMESTAMPTZ,

    closed_by               UUID,
    closed_at               TIMESTAMPTZ,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ADR-DATA-008 Karar 1: at most one open session per branch. 'opening_control'
-- is deliberately absent from this predicate: see the status column comment
-- above — no row is ever written in that status, so including it here would
-- be dead weight, not an oversight.
CREATE UNIQUE INDEX IF NOT EXISTS cash_sessions_one_open_per_branch
    ON cash_sessions (tenant_id, branch_id)
    WHERE status IN ('opened', 'closing_control');

CREATE INDEX IF NOT EXISTS cash_sessions_branch_idx
    ON cash_sessions (tenant_id, branch_id, opened_at DESC);

ALTER TABLE cash_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE cash_sessions FORCE ROW LEVEL SECURITY;

CREATE POLICY cash_sessions_tenant_isolation ON cash_sessions
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- ─── cash_movements ─────────────────────────────────────────────────────────
-- In-shift cash in/out (kasadan para alma, bozuk para koyma). Append-only
-- ledger: rows are never updated or deleted, matching this repo's event-
-- immutability convention (ADR-DATA-002) even though this is not an outbox
-- table — a correction is a new offsetting movement, not an edit.
CREATE TABLE IF NOT EXISTS cash_movements (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    branch_id       UUID        NOT NULL,
    session_id      UUID        NOT NULL,
    direction       TEXT        NOT NULL CHECK (direction IN ('in', 'out')),
    amount_minor    BIGINT      NOT NULL CHECK (amount_minor > 0),
    reason          TEXT        NOT NULL,
    created_by      UUID        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS cash_movements_session_idx
    ON cash_movements (tenant_id, session_id, created_at);

ALTER TABLE cash_movements ENABLE ROW LEVEL SECURITY;
ALTER TABLE cash_movements FORCE ROW LEVEL SECURITY;

CREATE POLICY cash_movements_tenant_isolation ON cash_movements
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON cash_sessions   TO app_runtime;
GRANT SELECT, INSERT         ON cash_movements  TO app_runtime;
