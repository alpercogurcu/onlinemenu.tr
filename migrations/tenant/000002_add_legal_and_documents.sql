-- Migration: tenant/000002_add_legal_and_documents
-- Extends the tenant module with:
--   1. Legal identity fields on tenants (tax, address, IBAN, MERSİS)
--   2. Branch operational fields (slug, ownership/operation type, geo-coordinates)
--   3. Branch-settings POS/billing/fiscal config columns
--   4. tenant_documents table for regulatory document storage (MinIO-backed)
--
-- Depends on:
--   tenant/000001_create_tenants   — tenants, branches, branch_settings tables
--   identity/000001_create_users   — users table (verified_by FK)
--
-- ADR references: ADR-SEC-001, ADR-SEC-002, ADR-FISCAL-001, ADR-DATA-003, ADR-OPS-002

-- ============================================================
-- SECTION 1 — tenants: legal identity fields
-- ============================================================

ALTER TABLE tenants
    -- Tüzel kişilik adı: fatura üzerinde yasal olarak yer alması gereken resmi ünvan.
    -- display name (tenants.name) ile farklı olabilir; e-fatura zorunluluğu nedeniyle ayrı tutulur.
    ADD COLUMN legal_name       TEXT,

    -- Ticaret unvanı: şirketin ticaret sicilindeki kayıtlı adı.
    -- Bazı belgeler (sözleşme, sipariş faturası) legal_name yerine trade_name gerektirir.
    ADD COLUMN trade_name       TEXT,

    -- Kimlik türü: 'kurumsal' → vergi numarası (10 hane), 'bireysel' → TCKN (11 hane).
    -- Türk e-fatura sistemi bu ayrımı zorunlu kılar (GİB uyumluluk).
    ADD COLUMN identity_type    TEXT NOT NULL DEFAULT 'kurumsal'
                                    CHECK (identity_type IN ('kurumsal', 'bireysel')),

    -- Vergi no veya TCKN. Unique index aşağıda tanımlanır.
    -- NULL izni: mevcut tenant'ların migration sırasında verileri eksik olabilir;
    -- uygulama katmanında onboarding tamamlanmadan aktif edilemez.
    ADD COLUMN tax_no           TEXT,

    -- Vergi dairesi adı (serbest metin). GİB'e bildirilen daire adı e-fatura XML'ine girer.
    ADD COLUMN tax_office       TEXT,

    -- MERSİS numarası: Türkiye Ticaret Sicili entegrasyon numarası.
    -- Büyük şirketler için zorunlu, KOBİ'lerde opsiyonel.
    ADD COLUMN mersis_no        TEXT,

    -- Şirket IBAN: tedarikçi ödemeleri ve otomatik muhasebe entegrasyonları için.
    -- Ödeme akışına doğrudan girmez; referans olarak tutulur.
    ADD COLUMN iban             TEXT,

    -- Yasal adres: fatura ve resmi yazışmalarda kullanılacak adres.
    ADD COLUMN address          TEXT,
    ADD COLUMN city             TEXT,
    ADD COLUMN district         TEXT,
    ADD COLUMN postal_code      TEXT,

    -- ISO 3166-1 alpha-2. Türkiye pazarı için 'TR' varsayılan.
    -- Çok ülkeli genişleme ihtimaline karşı sabit kodlanmaz.
    ADD COLUMN country          CHAR(2) NOT NULL DEFAULT 'TR',

    -- İletişim bilgileri: müşteri desteği ve muhasebe bildirimlerinde kullanılır.
    ADD COLUMN phone            TEXT,
    ADD COLUMN website          TEXT,

    -- Muhasebe/fatura iletişim e-postası; kullanıcı login e-postasından bağımsız olabilir.
    ADD COLUMN contact_email    TEXT;

-- Bir vergi numarası sistemde yalnızca bir tenant'a ait olabilir.
-- Partial index: NULL değerler unique constraint'e dahil edilmez (onboarding kolaylığı).
CREATE UNIQUE INDEX tenants_tax_no_idx
    ON tenants (tax_no)
    WHERE tax_no IS NOT NULL;

-- MERSİS numarası da tenant başına tekil olmalı.
CREATE UNIQUE INDEX tenants_mersis_no_idx
    ON tenants (mersis_no)
    WHERE mersis_no IS NOT NULL;

-- ============================================================
-- SECTION 2 — branches: drop obsolete columns first, then add new ones
-- ============================================================

-- timezone kolonu branch_settings.business_day_offset'e taşındı (ADR-DATA-003).
-- branches seviyesinde timezone tutmak tutarsızlığa yol açar;
-- tüm iş-günü sınırı hesapları branch_settings üzerinden yapılır.
ALTER TABLE branches DROP COLUMN IF EXISTS timezone;

ALTER TABLE branches
    -- Şubeye özgü URL dostu tanımlayıcı. Subdomain veya path routing'de kullanılır.
    -- Aynı tenant içinde tekil olmalı; global tekil olmak zorunda değil.
    ADD COLUMN slug             TEXT,

    -- Şube sahiplik modeli: 'sube' → doğrudan işletme, 'franchise' → bayilik.
    -- Tedarik kuralları (supply_rules) bu ayrıma göre filtrelenebilir.
    ADD COLUMN ownership_type   TEXT NOT NULL DEFAULT 'sube'
                                    CHECK (ownership_type IN ('sube', 'franchise')),

    -- İşletme türü: raporlama, menü şablonu ve vergi kodu seçimi bu alana göre ayrışır.
    ADD COLUMN operation_type   TEXT NOT NULL DEFAULT 'restoran'
                                    CHECK (operation_type IN (
                                        'restoran', 'bar', 'market',
                                        'food_truck', 'imalat', 'depo'
                                    )),

    -- Tedarik kuralları: JSON dizisi olarak saklanan şube bazlı sipariş kısıtları.
    -- Örn: belirli tedarikçiden yalnızca belirli ürün kategorisi sipariş edilebilir.
    -- Catalog modülündeki supply chain servisi bu alanı yorumlar.
    ADD COLUMN supply_rules     JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- Şube iletişim bilgileri: tenant.phone'dan bağımsız, şube müşteri hattı.
    ADD COLUMN phone            TEXT,

    -- Şube adres alanları (adres 000001'de zaten mevcut; buraya dokunulmaz).
    -- city, district, postal_code ise şimdi ekleniyor.
    ADD COLUMN city             TEXT,
    ADD COLUMN district         TEXT,
    ADD COLUMN postal_code      TEXT,

    -- Coğrafi konum: harita entegrasyonu ve teslimat alanı hesabı için.
    -- NUMERIC(9,6): enlem/boylam için yeterli hassasiyet (±0.000001° ≈ 0.11 m).
    ADD COLUMN latitude         NUMERIC(9,6),
    ADD COLUMN longitude        NUMERIC(9,6);

-- Slug, tenant içinde tekil — global tekil gerekmez.
-- Partial index: NULL slug'lar zorunlu değil (mevcut şubeler yavaşça doldurulur).
CREATE UNIQUE INDEX branches_tenant_slug_idx
    ON branches (tenant_id, slug)
    WHERE slug IS NOT NULL;

-- ============================================================
-- SECTION 3 — branch_settings: POS / billing / fiscal config
-- ============================================================

ALTER TABLE branch_settings
    -- Para birimi: şube seviyesinde tanımlanır; çok dövizli destek için.
    -- Mevcut Türkiye pazarı için 'TRY' varsayılan; ileride EUR, USD şubeler eklenebilir.
    ADD COLUMN currency             CHAR(3)     NOT NULL DEFAULT 'TRY',

    -- POS terminal tipi: donanım entegrasyon katmanının başlatacağı sürücüyü belirler.
    ADD COLUMN pos_terminal_type    TEXT        NOT NULL DEFAULT 'none'
                                        CHECK (pos_terminal_type IN ('ingenico', 'verifone', 'none')),

    -- POS donanım konfigürasyonu (IP, port, merchant id vb.) — sürücüye özel JSON.
    -- Vault'taki hassas bilgiler buraya yazılmaz; yalnızca non-secret config.
    ADD COLUMN pos_config           JSONB       NOT NULL DEFAULT '{}'::jsonb,

    -- Billing entegrasyon parametreleri (EDM, Paraşüt, Mikro, Logo API anahtarları hariç).
    -- API anahtarları Vault dynamic secret olarak enjekte edilir; burada JSON şema tutulur.
    ADD COLUMN billing_config       JSONB       NOT NULL DEFAULT '{}'::jsonb,

    -- Fiscal cihaz bağlantı parametreleri: port, baud rate, cihaz seri no vb.
    -- FiscalAdapter.RegisterSale() bu konfigürasyonu kullanır (ADR-FISCAL-001).
    ADD COLUMN fiscal_device_config JSONB       NOT NULL DEFAULT '{}'::jsonb,

    -- Şubenin varsayılan fiyat listesi. catalog/000001 migration'ından sonra FK eklenecek;
    -- şimdilik yalnızca ham UUID kolonu — referential integrity catalog migration'da sağlanır.
    -- FK'yı erken eklemek migration bağımlılık sırasını kırar (catalog henüz yok).
    ADD COLUMN default_price_list_id UUID;  -- FK: catalog/000002 ile eklenecek

-- fiscal_device_type check constraint'ini genişlet: Türkiye'deki gerçek ÖKC cihazlarını ekle.
-- Eski constraint (000001): ('none', 'mock', 'efatura', 'okc') — 'okc' çok genel, marka bazlı ayrım gerekli.
-- Yeni liste: ADR-FISCAL-001 ek-A'daki onaylı cihaz listesiyle hizalı.
ALTER TABLE branch_settings
    DROP CONSTRAINT IF EXISTS branch_settings_fiscal_device_type_check;

ALTER TABLE branch_settings
    ADD CONSTRAINT branch_settings_fiscal_device_type_check
        CHECK (fiscal_device_type IN (
            'none',         -- Sadece test/geliştirme ortamı
            'mock',         -- Integration test / staging mock adapter
            'efatura',      -- GİB e-fatura (API tabanlı, donanımsız)
            'okc',          -- Genel ÖKC (marka bilinmiyorsa geçiş değeri)
            'hugin',        -- Hugin ÖKC (yaygın Türkiye markası)
            'profilo',      -- Profilo ÖKC
            'beko',         -- Beko ÖKC
            'ingenico_yn',  -- Ingenico YN serisi (yeni nesil ÖKC)
            'verifone_yn'   -- Verifone YN serisi (yeni nesil ÖKC)
        ));

-- ============================================================
-- SECTION 4 — tenant_documents
-- ============================================================

-- Tenant'ın yüklediği resmi belgeler (vergi levhası, ticaret sicil vb.).
-- Asıl dosya MinIO'da object storage'da tutulur; bu tablo yalnızca metadata ve doğrulama durumunu saklar.
-- Mali nitelik taşıyan belgeler için hard delete yasaktır — soft delete (deleted_at) kullanılır (ADR-OPS-002 benzeri).
CREATE TABLE tenant_documents (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,

    -- Belge tipi: platform operatörünün doğrulama kuyruğunu ve saklama politikasını belirler.
    document_type   TEXT        NOT NULL
                        CHECK (document_type IN (
                            'vergi_levhasi',        -- Vergi Dairesi'nden alınan yıllık vergi levhası
                            'ticaret_sicil',         -- Ticaret sicil gazetesi ilanı
                            'imza_sirkuleri',        -- İmza sirküleri (yetkili imza listesi)
                            'faaliyet_belgesi',      -- Oda faaliyet belgesi (TSO/TESK)
                            'gida_sicil',            -- Gıda sicil belgesi (restoran/market zorunlu)
                            'isyeri_acma_ruhsati',   -- İşyeri açma ve çalışma ruhsatı
                            'other'                  -- Diğer (belge tipi ileride genişletilebilir)
                        )),

    -- MinIO nesne referansı. file_key uygulama katmanında oluşturulur;
    -- format: "tenants/<tenant_id>/<document_type>/<uuid>.<ext>"
    -- Nesneye doğrudan URL verilmez — her erişim signed URL ile yapılır (güvenlik).
    file_key        TEXT        NOT NULL,
    file_name       TEXT        NOT NULL,   -- Kullanıcıya gösterilen orijinal dosya adı
    file_size       BIGINT      NOT NULL,   -- Byte cinsinden; sıfır boyutlu yükleme kabul edilmez (uygulama katmanında validate)

    -- İzin verilen MIME tipleri: PDF ve yaygın görüntü formatları.
    -- Diğer formatlar (Word, Excel vb.) kabul edilmez — standartlaştırma ve virüs vektörü azaltma.
    mime_type       TEXT        NOT NULL
                        CHECK (mime_type IN (
                            'application/pdf',
                            'image/jpeg',
                            'image/png',
                            'image/webp'
                        )),

    -- Doğrulama durumu: platform operatörü belgeler doğrulamak zorunda.
    -- 'pending'  → yeni yükleme, inceleme bekliyor
    -- 'verified' → operatör onayladı
    -- 'rejected' → operatör reddetti (rejection_note zorunlu)
    -- 'expired'  → valid_until geçti (arka plan job'ı tarafından güncellenir)
    status          TEXT        NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'verified', 'rejected', 'expired')),

    -- Doğrulayan platform operatörü kullanıcısı.
    -- ON DELETE SET NULL: kullanıcı silinse bile belge kaydı korunmalı (mali kayıt).
    verified_by     UUID        REFERENCES users (id) ON DELETE SET NULL,
    verified_at     TIMESTAMPTZ,

    -- Reddedilme sebebi: 'rejected' durumunda operatörün tenant'a bildirdiği açıklama.
    rejection_note  TEXT,

    -- Geçerlilik aralığı: vergi levhası her yıl yenilenir; süresiz belgeler için valid_until NULL.
    valid_from      DATE,
    valid_until     DATE,       -- NULL → süresiz geçerli

    -- Soft delete: mali belge niteliği nedeniyle fiziksel silme yasaktır.
    -- Silinen belgeler RLS read policy'si tarafından gizlenir; audit log için korunur.
    deleted_at      TIMESTAMPTZ,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- RLS — ADR-SEC-001, ADR-SEC-002
ALTER TABLE tenant_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_documents FORCE ROW LEVEL SECURITY;

-- Tenant yalnızca kendi belgelerini görebilir; silinmiş belgeler select'ten çıkar.
-- Platform operatörü (superuser) RLS'i bypass eder — operatör doğrulama akışı buradan geçmez.
CREATE POLICY tenant_documents_read ON tenant_documents
    FOR SELECT TO app_runtime
    USING (
        tenant_id = current_setting('app.tenant_id', TRUE)::uuid
        AND deleted_at IS NULL
    );

-- Write policy: INSERT/UPDATE/DELETE için tenant_id hem mevcut satırda hem yeni satırda eşleşmeli.
-- Silme işlemi hard delete değil; uygulama deleted_at'ı günceller (UPDATE yolu).
CREATE POLICY tenant_documents_write ON tenant_documents
    FOR ALL TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

-- Index'ler
-- Tenant'ın tüm aktif belgelerini listeleyen ana sorgu.
CREATE INDEX tenant_docs_tenant_idx
    ON tenant_documents (tenant_id)
    WHERE deleted_at IS NULL;

-- Belge tipi bazlı filtreleme (tenant belgelerini tipe göre listele).
CREATE INDEX tenant_docs_type_idx
    ON tenant_documents (tenant_id, document_type)
    WHERE deleted_at IS NULL;

-- Platform operatörü doğrulama kuyruğu: yalnızca 'pending' satırları.
-- Partial index sayesinde tüm tabloyu taramak yerine küçük bir set üzerinde çalışır.
CREATE INDEX tenant_docs_status_idx
    ON tenant_documents (status)
    WHERE status = 'pending';

-- Yaklaşan sona erme takibi: arka plan job'ı bu index'i kullanarak 'expired' statusüne geçirir.
CREATE INDEX tenant_docs_expiry_idx
    ON tenant_documents (valid_until)
    WHERE valid_until IS NOT NULL AND deleted_at IS NULL;

COMMENT ON TABLE tenant_documents IS
    'Tenant''ın platform tarafından doğrulanan yasal belgeleri (vergi levhası, ticaret sicil vb.). '
    'Dosyalar MinIO''da saklanır; bu tablo yalnızca metadata ve doğrulama durumunu tutar. '
    'Mali nitelik taşıdığından hard delete yasaktır — soft delete (deleted_at) uygulanır.';
