-- Storefront module: QR dine-in online ordering (ADR-ARCH-006).
-- branch_id / table_id / order_id / check_id are bare UUIDs on purpose: they
-- belong to pos, and cross-module foreign keys are forbidden
-- (migrations/pos/000001, migrations/identity/000011_drop_cross_module_fks).
-- Existence is validated through pos/public, never by the database.

CREATE TABLE storefront_qr_codes (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NOT NULL,
    branch_id   UUID        NOT NULL,
    table_id    UUID        NOT NULL,
    table_label TEXT        NOT NULL DEFAULT '',
    -- Lowercase hex SHA-256 of the raw token. The raw token is never written
    -- here: it is returned exactly once, in the issue/rotate response, so a
    -- database read (or a leaked dump) cannot reconstruct a working QR code.
    token_hash  TEXT        NOT NULL,
    status      TEXT        NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active', 'revoked')),
    created_by  UUID        NOT NULL,
    revoked_at  TIMESTAMPTZ,
    revoked_by  UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX storefront_qr_codes_token_hash_uidx ON storefront_qr_codes (token_hash);
CREATE INDEX storefront_qr_codes_tenant_branch_idx ON storefront_qr_codes (tenant_id, branch_id);
CREATE UNIQUE INDEX storefront_qr_codes_active_table_uidx
    ON storefront_qr_codes (table_id) WHERE status = 'active';

ALTER TABLE storefront_qr_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_qr_codes FORCE ROW LEVEL SECURITY;

-- Token -> tenant resolution has to happen BEFORE the tenant is known, so the
-- usual tenant_isolation policy alone would deny the bootstrap read. Rather
-- than reusing the app.tenant_scope = 'all_tenants' branch (which opens the
-- whole table) or a SECURITY DEFINER function (whose owner app_migrator holds
-- BYPASSRLS — deploy/postgres/init.sql), this SELECT branch keys on a GUC that
-- carries the token hash itself: it can only ever unlock the single row whose
-- hash the caller already possesses. The only writer of that GUC is
-- platform/db.WithQRTokenLookupTx, which opens a read-only transaction and
-- never sets app.tenant_id (ADR-ARCH-006 §4).
--
-- status is deliberately NOT part of the predicate: a revoked code must stay
-- readable here so the service layer can tell "revoked" from "never existed"
-- and keep an audit trail; both map to 404 at the HTTP edge.
CREATE POLICY qr_codes_read ON storefront_qr_codes
    FOR SELECT TO app_runtime
    USING (
        tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid
        OR token_hash = NULLIF(current_setting('app.storefront_qr_token_hash', TRUE), '')
    );

CREATE POLICY qr_codes_write ON storefront_qr_codes
    FOR ALL TO app_runtime
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON storefront_qr_codes TO app_runtime;

-- storefront_guest_orders binds a placed pos order to the guest session that
-- placed it. Without it a guest could poll any order id it guessed; with it,
-- "my orders" is a join on the session claim in the guest JWT (ADR-ARCH-006 §8).
CREATE TABLE storefront_guest_orders (
    order_id         UUID        PRIMARY KEY,
    tenant_id        UUID        NOT NULL,
    qr_code_id       UUID        NOT NULL REFERENCES storefront_qr_codes (id) ON DELETE RESTRICT,
    guest_session_id UUID        NOT NULL,
    check_id         UUID        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX storefront_guest_orders_session_idx ON storefront_guest_orders (tenant_id, guest_session_id);

ALTER TABLE storefront_guest_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_guest_orders FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON storefront_guest_orders
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT ON storefront_guest_orders TO app_runtime;
