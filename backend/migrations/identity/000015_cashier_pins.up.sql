-- Migration: identity/000015_cashier_pins
-- ADR-DATA-008 "PIN akışının ayrıntıları (2026-08-03 eki)": server-verified
-- cashier PIN switching. Scope is (person, tenant) — not per-person (a
-- single-realm person can work at two tenants, ADR-AUTH-002) and not per
-- membership (the same person at two branches of one tenant would otherwise
-- carry two PINs, a pure memorisation burden with no security benefit).
--
-- Storage is argon2id, per-row salt. The PIN itself never reaches the
-- client in any form, including hashed — this table is written and read
-- exclusively by identity/service/pin.go; no other module, and no HTTP
-- handler anywhere, ever selects pin_hash or pin_salt.

SET LOCAL role = app_migrator;

CREATE TABLE IF NOT EXISTS cashier_pins (
    tenant_id   UUID        NOT NULL,
    person_id   UUID        NOT NULL,
    pin_salt    BYTEA       NOT NULL,
    pin_hash    BYTEA       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, person_id)
);

ALTER TABLE cashier_pins ENABLE ROW LEVEL SECURITY;
ALTER TABLE cashier_pins FORCE ROW LEVEL SECURITY;

CREATE POLICY cashier_pins_tenant_isolation ON cashier_pins
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

-- DELETE is required: ResetPin (manager action) removes the row outright —
-- there is no "cleared" sentinel value, the row's absence IS the cleared
-- state, and the person sets a fresh PIN (fresh salt, fresh hash) at their
-- next join. There is deliberately no SELECT grant restriction narrower than
-- the RLS policy above; the actual "manager cannot read a PIN" guarantee is
-- enforced in Go (identity/service/pin.go exposes no read-by-other-person
-- path, and the public.CashierPinService contract other modules consume has
-- no method that returns a hash or a PIN) — RLS only stops cross-tenant
-- reads, which is all it is for (ADR-AUTH-001 layer 1).
GRANT SELECT, INSERT, UPDATE, DELETE ON cashier_pins TO app_runtime;
