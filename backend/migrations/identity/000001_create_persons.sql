-- Migration: identity/000001_create_persons
-- Platform-level person entity. No tenant_id: a person can belong to multiple tenants.
-- RLS policies that reference the memberships table are deferred to 000003, because
-- PostgreSQL binds policy expressions at CREATE POLICY time and memberships does not
-- exist yet. Only ENABLE/FORCE RLS and the insert policy (no subquery) are created here.

CREATE TABLE persons (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    keycloak_sub TEXT        NOT NULL,
    email        TEXT        NOT NULL,
    full_name    TEXT        NOT NULL DEFAULT '',
    phone        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX persons_keycloak_sub_idx ON persons (keycloak_sub);
CREATE UNIQUE INDEX persons_email_idx        ON persons (email);

ALTER TABLE persons ENABLE ROW LEVEL SECURITY;
ALTER TABLE persons FORCE ROW LEVEL SECURITY;

-- Insert is open to app_runtime: person creation happens before any membership exists.
-- Service layer enforces that callers are authenticated (no anonymous person creation).
CREATE POLICY persons_insert ON persons FOR INSERT TO app_runtime
    WITH CHECK (true);

-- SELECT and UPDATE policies referencing memberships are added in 000003_create_memberships.sql
-- after the memberships table exists.
