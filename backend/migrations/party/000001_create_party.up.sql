-- Party module: suppliers, customers (cari hesap).
-- All tables enforce RLS.

SET LOCAL role = app_migrator;

-- ─── parties ─────────────────────────────────────────────────────────────────
-- A party is any external entity (supplier or customer) the tenant trades with.
CREATE TABLE IF NOT EXISTS parties (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    party_type      TEXT        NOT NULL
        CHECK (party_type IN ('supplier', 'customer', 'both')),
    name            TEXT        NOT NULL,
    short_name      TEXT        NOT NULL DEFAULT '',
    tax_no          TEXT        NOT NULL DEFAULT '',
    tax_office      TEXT        NOT NULL DEFAULT '',
    -- GİB e-invoice alias (for billing integration)
    gib_alias       TEXT        NOT NULL DEFAULT '',
    -- contact
    phone           TEXT        NOT NULL DEFAULT '',
    email           TEXT        NOT NULL DEFAULT '',
    website         TEXT        NOT NULL DEFAULT '',
    -- address
    address_line    TEXT        NOT NULL DEFAULT '',
    city            TEXT        NOT NULL DEFAULT '',
    district        TEXT        NOT NULL DEFAULT '',
    postal_code     TEXT        NOT NULL DEFAULT '',
    -- financials
    payment_terms_days  INT     NOT NULL DEFAULT 0,
    credit_limit_amount BIGINT  NOT NULL DEFAULT 0,  -- kuruş
    currency        TEXT        NOT NULL DEFAULT 'TRY',
    -- status
    is_active       BOOL        NOT NULL DEFAULT TRUE,
    notes           TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS parties_tenant_type_idx ON parties (tenant_id, party_type);
CREATE INDEX IF NOT EXISTS parties_tenant_name_idx ON parties (tenant_id, name);
CREATE INDEX IF NOT EXISTS parties_tax_no_idx ON parties (tenant_id, tax_no)
    WHERE tax_no <> '';

ALTER TABLE parties ENABLE ROW LEVEL SECURITY;
ALTER TABLE parties FORCE ROW LEVEL SECURITY;

CREATE POLICY parties_tenant_isolation ON parties
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON parties TO app_runtime;

-- ─── party_contacts ───────────────────────────────────────────────────────────
-- Additional contact persons for a party (salesperson, accountant, etc.)
CREATE TABLE IF NOT EXISTS party_contacts (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NOT NULL,
    party_id    UUID        NOT NULL,
    name        TEXT        NOT NULL,
    role        TEXT        NOT NULL DEFAULT '',   -- e.g. 'satış', 'muhasebe'
    phone       TEXT        NOT NULL DEFAULT '',
    email       TEXT        NOT NULL DEFAULT '',
    is_primary  BOOL        NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS party_contacts_party_idx ON party_contacts (party_id);

ALTER TABLE party_contacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE party_contacts FORCE ROW LEVEL SECURITY;

CREATE POLICY party_contacts_tenant_isolation ON party_contacts
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON party_contacts TO app_runtime;
