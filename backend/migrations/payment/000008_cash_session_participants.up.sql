-- Migration: payment/000008_cash_session_participants
-- ADR-DATA-008 "PIN akışının ayrıntıları (2026-08-03 eki)" §4: a cashier can
-- only be PIN-selected on a cash session they have joined at least once via
-- the full Keycloak flow. This table is that participation record.
--
-- Lives in `payment`, not `identity`, even though "who joined" reads like an
-- identity question: the row's natural key is (session_id, person_id) and
-- session_id is payment's cash_sessions — the same "the resource this data
-- is about already lives here" reasoning migration 000007's header uses for
-- cash_movements. identity stays completely unaware of cash sessions; it
-- only exposes pub.CashierPinService (PIN storage/verification), which this
-- module's service layer calls.

SET LOCAL role = app_migrator;

CREATE TABLE IF NOT EXISTS cash_session_participants (
    tenant_id   UUID        NOT NULL,
    session_id  UUID        NOT NULL,
    branch_id   UUID        NOT NULL,
    person_id   UUID        NOT NULL,
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, session_id, person_id)
);

CREATE INDEX IF NOT EXISTS cash_session_participants_session_idx
    ON cash_session_participants (tenant_id, session_id);

ALTER TABLE cash_session_participants ENABLE ROW LEVEL SECURITY;
ALTER TABLE cash_session_participants FORCE ROW LEVEL SECURITY;

CREATE POLICY cash_session_participants_tenant_isolation ON cash_session_participants
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- UPDATE is granted (not just INSERT) so re-joining an existing participant
-- can refresh joined_at via ON CONFLICT DO UPDATE — this is not an outbox
-- table (ADR-DATA-002's "no UPDATE on outbox payload" rule does not apply
-- here), and re-joining is exactly the ADR §5 mechanism that clears a
-- person's PIN lockout, so it must actually happen, not silently no-op.
GRANT SELECT, INSERT, UPDATE ON cash_session_participants TO app_runtime;
