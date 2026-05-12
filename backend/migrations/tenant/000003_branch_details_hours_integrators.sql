-- Migration: tenant/000003_branch_details_hours_integrators
-- Extends the tenant module with:
--   1. branches: franchise legal identity fields (IBAN, legal_name, tax_no, tax_office)
--   2. branch_documents: branch-scoped regulatory document storage (parallel to tenant_documents)
--   3. billing_integrators: tenant/branch-level e-invoice integrator configuration (Vault-backed)
--   4. branch_regular_hours: weekly recurring schedule with multi-slot support per day
--   5. branch_special_hours: date-specific overrides (holidays, special events)
--
-- Depends on:
--   tenant/000001_create_tenants   — tenants, branches tables
--   tenant/000002_add_legal_and_documents — users FK exists via identity/000001_create_users
--
-- ADR references: ADR-SEC-001, ADR-SEC-002, ADR-FISCAL-001, ADR-OPS-002, ADR-DATA-003

-- ============================================================
-- SECTION 1 — branches: franchise legal identity + IBAN
-- ============================================================
-- Franchise branches may have their own legal entity distinct from the parent tenant.
-- tax_no here refers to the branch's own tax number, which differs from tenants.tax_no
-- for franchise arrangements where a franchisee operates under a separate legal identity.

ALTER TABLE branches
    -- Franchisee IBAN for automated accounting reconciliation with the branch entity.
    ADD COLUMN iban             TEXT,

    -- Legal registered name of the franchise entity operating this branch.
    -- May differ from tenants.legal_name when ownership_type = 'franchise'.
    -- NULL for directly-operated branches (ownership_type = 'sube').
    ADD COLUMN legal_name       TEXT,

    -- Identity classification mirrors tenants.identity_type; required for e-invoice XML (GIB compliance).
    ADD COLUMN identity_type    TEXT NOT NULL DEFAULT 'kurumsal'
                                    CHECK (identity_type IN ('kurumsal', 'bireysel')),

    -- Tax number (10-digit kurumsal) or TCKN (11-digit bireysel) of the branch legal entity.
    -- NULL for directly-operated branches that inherit tenant tax identity.
    ADD COLUMN tax_no           TEXT,

    -- Tax office name as registered with GIB; appears in e-invoice XML.
    ADD COLUMN tax_office       TEXT;

-- A tax number must belong to at most one branch across all tenants.
-- Partial index excludes NULLs so directly-operated branches (no separate tax_no) are not constrained.
CREATE UNIQUE INDEX branches_tax_no_idx
    ON branches (tax_no)
    WHERE tax_no IS NOT NULL;

-- ============================================================
-- SECTION 2 — branch_documents
-- ============================================================
-- Branch-scoped regulatory documents. Mirrors tenant_documents structure but adds
-- branch-specific document types (kira_sozlesmesi, franchise_sozlesmesi, yangin_guvenlik).
-- Files are stored in MinIO under the tenant-documents bucket; this table holds metadata only.
-- Hard delete is forbidden for legally significant documents — soft delete (deleted_at) applies.

CREATE TABLE branch_documents (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    branch_id       UUID        NOT NULL REFERENCES branches(id) ON DELETE CASCADE,

    -- Document classification determines storage policy and operator verification queue routing.
    document_type   TEXT        NOT NULL
                        CHECK (document_type IN (
                            'vergi_levhasi',
                            'isyeri_ruhsati',           -- Branch-specific business operation licence
                            'kira_sozlesmesi',           -- Branch lease agreement
                            'franchise_sozlesmesi',      -- Franchise agreement (ownership_type='franchise')
                            'gida_sicil',
                            'isyeri_acma_ruhsati',
                            'yangin_guvenlik',           -- Fire safety certificate (restaurant/market)
                            'saglik_sertifikasi',        -- Batch personnel health certificates
                            'other'
                        )),

    -- MinIO object key. Format: "branches/<branch_id>/<document_type>/<uuid>.<ext>"
    -- Never expose directly; always serve via signed URL.
    file_key        TEXT        NOT NULL,
    file_name       TEXT        NOT NULL,
    file_size       BIGINT      NOT NULL,
    mime_type       TEXT        NOT NULL
                        CHECK (mime_type IN ('application/pdf', 'image/jpeg', 'image/png', 'image/webp')),

    -- Verification lifecycle: pending → verified | rejected; background job sets expired.
    status          TEXT        NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'verified', 'rejected', 'expired')),

    -- Platform operator who reviewed the document. SET NULL on user deletion to preserve audit trail.
    verified_by     UUID        REFERENCES users(id) ON DELETE SET NULL,
    verified_at     TIMESTAMPTZ,

    -- Mandatory when status = 'rejected'; describes why the document was not accepted.
    rejection_note  TEXT,

    valid_from      DATE,
    valid_until     DATE,       -- NULL → no expiry

    deleted_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE branch_documents IS
    'Şubeye ait yasal belgeler (kira, ruhsat, franchise sözleşmesi vb.). '
    'Asıl dosya MinIO/tenant-documents bucket''ında saklanır; bu tablo yalnızca metadata ve doğrulama durumunu tutar. '
    'Mali nitelik taşıdığından hard delete yasaktır — soft delete (deleted_at) uygulanır.';

ALTER TABLE branch_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE branch_documents FORCE ROW LEVEL SECURITY;

-- Read: tenant sees only its own branches' documents; deleted records are hidden.
CREATE POLICY branch_docs_read ON branch_documents FOR SELECT TO app_runtime
    USING (tenant_id = current_setting('app.tenant_id', TRUE)::uuid AND deleted_at IS NULL);

-- Write: INSERT/UPDATE/DELETE both the existing row and the new row must belong to the active tenant.
-- Deletion is performed as an UPDATE setting deleted_at (soft delete), not as a hard DELETE.
CREATE POLICY branch_docs_write ON branch_documents FOR ALL TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

-- Primary listing query: all active documents for a given branch.
CREATE INDEX branch_docs_branch_idx
    ON branch_documents (branch_id)
    WHERE deleted_at IS NULL;

-- Filtered listing by document type within a branch.
CREATE INDEX branch_docs_type_idx
    ON branch_documents (branch_id, document_type)
    WHERE deleted_at IS NULL;

-- Platform operator verification queue: only pending rows, small working set.
CREATE INDEX branch_docs_status_idx
    ON branch_documents (status)
    WHERE status = 'pending';

-- Background job expiry sweep: only rows with a finite validity window.
CREATE INDEX branch_docs_expiry_idx
    ON branch_documents (valid_until)
    WHERE valid_until IS NOT NULL AND deleted_at IS NULL;

-- ============================================================
-- SECTION 3 — billing_integrators
-- ============================================================
-- Stores non-sensitive e-invoice integrator configuration at tenant or branch level.
-- branch_id IS NULL  → tenant-wide default (all branches inherit unless overridden).
-- branch_id NOT NULL → branch-specific override (franchise branches with own integrator).
-- Sensitive credentials (API keys, passwords) live in HashiCorp Vault.
-- vault_secret_path records where to fetch them; is_active must be FALSE when path is NULL.

CREATE TABLE billing_integrators (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- NULL → tenant-wide configuration.
    -- NOT NULL → overrides tenant default for this specific branch.
    -- Franchise branches commonly carry their own GIB-registered e-invoice mailbox.
    branch_id           UUID        REFERENCES branches(id) ON DELETE CASCADE,

    -- Integrator provider slug. Only 'edm' is active in Phase 1;
    -- remaining providers are registered here to allow schema-level constraint without a Phase 2 migration.
    provider            TEXT        NOT NULL
                            CHECK (provider IN (
                                'edm',              -- Electronic Document Management (Phase 1 active)
                                'parasut',           -- Paraşüt (Phase 2)
                                'mikro',             -- Mikro (Phase 2)
                                'logo',              -- Logo (Phase 2)
                                'izibiz',            -- İzibiz (Phase 2)
                                'digital_planet'     -- Digital Planet (Phase 2)
                            )),

    -- Human-readable label shown in the admin UI (e.g. "Ana EDM Hesabı", "Şube EDM").
    display_name        TEXT        NOT NULL,

    -- Non-sensitive configuration: company code, GIB endpoint, mailbox alias, certificate thumbprint, etc.
    -- Sensitive fields (API key, password) are NOT stored here; only vault_secret_path is written.
    config              JSONB       NOT NULL DEFAULT '{}'::jsonb,

    -- Vault KV v2 path where credentials are stored.
    -- Convention: "secret/data/billing/<tenant_id>/<provider>" (or ".../<branch_id>/...")
    -- NULL means credentials have not been entered yet; is_active must remain FALSE.
    vault_secret_path   TEXT,

    -- GIB-registered e-invoice mailbox (e.g. "urn:mail:info@acmekafe.com.tr").
    -- Must correspond to the tax number on the tenant or branch record.
    efatura_alias       TEXT,

    -- Environment separation: 'test' uses GIB test gateway; 'production' requires GIB approval.
    -- Prevents accidental production e-invoice submission during integration testing.
    environment         TEXT        NOT NULL DEFAULT 'test'
                            CHECK (environment IN ('test', 'production')),

    -- Only TRUE when vault_secret_path is set and connection test has succeeded.
    -- Application layer is responsible for toggling this flag after a successful health check.
    is_active           BOOLEAN     NOT NULL DEFAULT FALSE,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at          TIMESTAMPTZ
);

COMMENT ON TABLE billing_integrators IS
    'Tenant veya şube düzeyinde e-fatura entegratör konfigürasyonu. '
    'Credential''lar (API anahtarı, parola) Vault''ta saklanır; bu tablo yalnızca non-sensitive config ve Vault path tutar. '
    'branch_id IS NULL → tenant geneli varsayılan; branch_id dolu → franchise şubeye özel override.';

ALTER TABLE billing_integrators ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_integrators FORCE ROW LEVEL SECURITY;

CREATE POLICY billing_int_read ON billing_integrators FOR SELECT TO app_runtime
    USING (tenant_id = current_setting('app.tenant_id', TRUE)::uuid AND deleted_at IS NULL);

CREATE POLICY billing_int_write ON billing_integrators FOR ALL TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

-- Tenant-level uniqueness: at most one active record per provider at tenant scope.
-- Partial index on branch_id IS NULL separates tenant-level from branch-level rows.
CREATE UNIQUE INDEX billing_int_tenant_provider_idx
    ON billing_integrators (tenant_id, provider)
    WHERE branch_id IS NULL AND deleted_at IS NULL;

-- Branch-level uniqueness: at most one active record per provider per branch.
CREATE UNIQUE INDEX billing_int_branch_provider_idx
    ON billing_integrators (branch_id, provider)
    WHERE branch_id IS NOT NULL AND deleted_at IS NULL;

-- Full tenant listing (admin overview page).
CREATE INDEX billing_int_tenant_idx
    ON billing_integrators (tenant_id)
    WHERE deleted_at IS NULL;

-- Branch-specific lookup (used when resolving the effective integrator for a branch).
CREATE INDEX billing_int_branch_idx
    ON billing_integrators (branch_id)
    WHERE branch_id IS NOT NULL AND deleted_at IS NULL;

-- ============================================================
-- SECTION 4 — branch_regular_hours
-- ============================================================
-- Google Maps-style weekly recurring schedule. Multiple slots per day are supported
-- to accommodate restaurants with midday closures (e.g. 12:00-15:00 and 18:00-23:00).
-- Day numbering follows ISO 8601: 1 = Monday, 7 = Sunday.
-- All times are in the branch's local timezone (branch_settings.business_day_offset context).
-- Midnight-crossing slots (e.g. 22:00-02:00 at a bar) use crosses_midnight = TRUE;
-- close_time then belongs to the following calendar day.

CREATE TABLE branch_regular_hours (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    branch_id       UUID        NOT NULL REFERENCES branches(id) ON DELETE CASCADE,

    -- ISO 8601 day number. 1 = Monday, 7 = Sunday.
    day_of_week     SMALLINT    NOT NULL CHECK (day_of_week BETWEEN 1 AND 7),

    -- Both NULL when is_closed = TRUE. Application layer must enforce this invariant.
    -- open_time and close_time stored as TIME (no timezone); branch timezone applied at query time.
    open_time       TIME,
    close_time      TIME,

    -- TRUE when close_time nominally falls on the next calendar day (bar/nightclub pattern).
    -- The business logic layer interprets close_time relative to day_of_week + 1 when TRUE.
    crosses_midnight BOOLEAN     NOT NULL DEFAULT FALSE,

    -- TRUE means the branch is closed all day; open_time and close_time are ignored.
    is_closed       BOOLEAN     NOT NULL DEFAULT FALSE,

    -- Slot ordering within the same day (0 = first slot, 1 = second slot, etc.).
    -- Allows representing split schedules (e.g. lunch + dinner service windows).
    sort_order      SMALLINT    NOT NULL DEFAULT 0,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- A branch cannot have two slots at the same position for the same day.
    CONSTRAINT branch_regular_hours_unique UNIQUE (branch_id, day_of_week, sort_order)
);

COMMENT ON TABLE branch_regular_hours IS
    'Şubenin rutin haftalık çalışma saati programı. '
    'Gün başına birden fazla slot tanımlanabilir (öğle arası kapanan restoranlar). '
    'Günler ISO 8601 numaralandırmasıyla tutulur: 1=Pazartesi, 7=Pazar. '
    'Gece yarısı geçen slotlar (bar/gece kulübü) crosses_midnight=TRUE ile işaretlenir.';

ALTER TABLE branch_regular_hours ENABLE ROW LEVEL SECURITY;
ALTER TABLE branch_regular_hours FORCE ROW LEVEL SECURITY;

-- Read policy intentionally does NOT filter deleted_at — this table has no soft delete.
-- Rows are replaced wholesale when the schedule is updated.
CREATE POLICY branch_hours_read ON branch_regular_hours FOR SELECT TO app_runtime
    USING (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

CREATE POLICY branch_hours_write ON branch_regular_hours FOR ALL TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

-- Weekly schedule lookup by branch: covers both full-week fetch and single-day lookup.
CREATE INDEX branch_regular_hours_branch_idx
    ON branch_regular_hours (branch_id, day_of_week);

-- ============================================================
-- SECTION 5 — branch_special_hours
-- ============================================================
-- Date-specific schedule overrides that take precedence over regular_hours.
-- Evaluated first in the "is branch open now?" resolution order:
--   special_hours (exact date match) → regular_hours (day-of-week) → closed.
-- One record per branch per date enforced via unique constraint.
-- Common use cases: national holidays, Ramadan schedules, renovation closures, special events.

CREATE TABLE branch_special_hours (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    branch_id       UUID        NOT NULL REFERENCES branches(id) ON DELETE CASCADE,

    -- The calendar date this override applies to (branch-local date, no timezone stored here).
    special_date    DATE        NOT NULL,

    -- Human-readable label shown in admin UI and optionally surfaced to customers.
    -- Examples: "Kurban Bayramı 1. Günü", "Yılbaşı Gecesi", "Tadilat - Kapalı"
    name            TEXT,

    open_time       TIME,
    close_time      TIME,
    crosses_midnight BOOLEAN     NOT NULL DEFAULT FALSE,

    -- When TRUE, open_time/close_time are ignored; branch is closed for the entire day.
    is_closed       BOOLEAN     NOT NULL DEFAULT FALSE,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Each branch may have at most one override per calendar date.
    CONSTRAINT branch_special_hours_unique UNIQUE (branch_id, special_date)
);

COMMENT ON TABLE branch_special_hours IS
    'Bayram, tatil ve özel gün çalışma saati override''ları. '
    'regular_hours''dan önce değerlendirilir: eşleşen özel gün varsa regular_hours dikkate alınmaz. '
    'Şube başına tarih başına tek kayıt zorunluluğu UNIQUE constraint ile sağlanır.';

ALTER TABLE branch_special_hours ENABLE ROW LEVEL SECURITY;
ALTER TABLE branch_special_hours FORCE ROW LEVEL SECURITY;

CREATE POLICY branch_special_hours_read ON branch_special_hours FOR SELECT TO app_runtime
    USING (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

CREATE POLICY branch_special_hours_write ON branch_special_hours FOR ALL TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

-- Primary lookup: fetch a branch's special override for a specific date or date range.
CREATE INDEX branch_special_hours_date_idx
    ON branch_special_hours (branch_id, special_date);

-- Admin UI "upcoming holidays" panel and background pre-warming of schedule cache.
-- Filtered to future/today dates to keep the working set small as the table grows over years.
CREATE INDEX branch_special_hours_upcoming_idx
    ON branch_special_hours (special_date)
    WHERE special_date >= CURRENT_DATE;
