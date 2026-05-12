-- Migration: tenant/000001_create_tenants
-- Creates the core multi-tenancy tables: tenants, branches, and branch_settings.
-- Every table enforces FORCE ROW LEVEL SECURITY so even the table owner (app_migrator)
-- cannot bypass policies — only the superuser can (ADR-SEC-001, ADR-SEC-002).

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ============================================================
-- tenants
-- ============================================================
CREATE TABLE tenants (
    id               UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    name             TEXT        NOT NULL,
    slug             TEXT        NOT NULL,
    plan             TEXT        NOT NULL CHECK (plan IN ('starter', 'pro', 'enterprise')),
    enabled_modules  JSONB       NOT NULL DEFAULT '[]'::jsonb,
    is_active        BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Slug must be globally unique (used in subdomain routing).
CREATE UNIQUE INDEX tenants_slug_idx ON tenants (slug);

ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE ROW LEVEL SECURITY;

-- app_runtime may only see its own tenant row.
CREATE POLICY tenant_read  ON tenants FOR SELECT TO app_runtime
    USING  (id = current_setting('app.tenant_id', TRUE)::uuid);

CREATE POLICY tenant_write ON tenants FOR ALL    TO app_runtime
    USING  (id = current_setting('app.tenant_id', TRUE)::uuid)
    WITH CHECK (id = current_setting('app.tenant_id', TRUE)::uuid);

-- ============================================================
-- branches
-- ============================================================
CREATE TABLE branches (
    id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id   UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    address     TEXT,
    timezone    TEXT        NOT NULL DEFAULT 'Europe/Istanbul',
    is_active   BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX branches_tenant_idx ON branches (tenant_id);

ALTER TABLE branches ENABLE ROW LEVEL SECURITY;
ALTER TABLE branches FORCE ROW LEVEL SECURITY;

CREATE POLICY branch_read  ON branches FOR SELECT TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

CREATE POLICY branch_write ON branches FOR ALL    TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

-- ============================================================
-- branch_settings
-- Branch-level configuration that affects billing, fiscal, and POS behaviour.
-- ============================================================
CREATE TABLE branch_settings (
    id                   UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    branch_id            UUID        NOT NULL REFERENCES branches (id) ON DELETE CASCADE,
    tenant_id            UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,

    -- Billing provider slug (e.g. 'stripe', 'iyzico', 'paytm'). Faz 2.
    billing_provider     TEXT        NOT NULL DEFAULT 'none',

    -- Fiscal device type. 'none' is forbidden in production (ADR-FISCAL-001).
    -- Valid values: 'none', 'mock', 'efatura', 'okc'.
    fiscal_device_type   TEXT        NOT NULL DEFAULT 'none'
                            CHECK (fiscal_device_type IN ('none', 'mock', 'efatura', 'okc')),

    -- Business day boundary in minutes from midnight (e.g. 240 = 04:00 local time).
    -- See ADR-DATA-003.
    business_day_offset  INT         NOT NULL DEFAULT 240
                            CHECK (business_day_offset >= 0 AND business_day_offset < 1440),

    -- Tax rate applied to sales at this branch (stored as integer basis points, e.g. 1800 = 18%).
    tax_rate_bps         INT         NOT NULL DEFAULT 1800
                            CHECK (tax_rate_bps >= 0 AND tax_rate_bps <= 10000),

    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (branch_id)
);

CREATE INDEX branch_settings_tenant_idx ON branch_settings (tenant_id);

ALTER TABLE branch_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE branch_settings FORCE ROW LEVEL SECURITY;

CREATE POLICY branch_settings_read  ON branch_settings FOR SELECT TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

CREATE POLICY branch_settings_write ON branch_settings FOR ALL    TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);
