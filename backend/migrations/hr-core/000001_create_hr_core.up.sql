-- HR-Core module: tenant-scoped employee profiles.
-- Identity module owns persons (platform-level). HR-core owns the employment record.
-- PDKS (time-attendance) will be added in Faz 3 as a separate module.

SET LOCAL role = app_migrator;

-- ─── employee_profiles ────────────────────────────────────────────────────────
-- Stores the HR record for a person within a specific tenant.
-- One person can work for multiple tenants (each has its own row here).
-- Branch assignment and role management are handled by identity/memberships.
CREATE TABLE IF NOT EXISTS employee_profiles (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id        UUID        NOT NULL REFERENCES persons(id) ON DELETE RESTRICT,
    tenant_id        UUID        NOT NULL,

    -- Employment details
    department       TEXT        NOT NULL DEFAULT '',
    job_title        TEXT        NOT NULL DEFAULT '',
    employment_type  TEXT        NOT NULL DEFAULT 'full_time'
                         CHECK (employment_type IN ('full_time', 'part_time', 'seasonal', 'contractor')),

    -- Legal identity (KVKK: only salted hash stored, never plaintext)
    tc_kimlik_hash   TEXT        NOT NULL DEFAULT '',

    -- Timeline
    hire_date        DATE        NOT NULL,
    termination_date DATE,

    -- JSON fields for flexible contact/emergency data
    contact_info     JSONB       NOT NULL DEFAULT '{}',
    emergency_contact JSONB      NOT NULL DEFAULT '{}',

    -- Status
    status           TEXT        NOT NULL DEFAULT 'active'
                         CHECK (status IN ('active', 'on_leave', 'terminated')),
    notes            TEXT        NOT NULL DEFAULT '',

    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One employment record per person per tenant.
    CONSTRAINT employee_profiles_unique_person_tenant UNIQUE (person_id, tenant_id)
);

CREATE INDEX IF NOT EXISTS employee_profiles_tenant_idx
    ON employee_profiles (tenant_id, status);

CREATE INDEX IF NOT EXISTS employee_profiles_person_idx
    ON employee_profiles (person_id);

ALTER TABLE employee_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE employee_profiles FORCE ROW LEVEL SECURITY;

CREATE POLICY employee_profiles_tenant_isolation ON employee_profiles
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON employee_profiles TO app_runtime;
