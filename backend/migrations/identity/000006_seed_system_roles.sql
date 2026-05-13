-- Migration: identity/000006_seed_system_roles
-- Seeds platform-wide default roles (tenant_id IS NULL, is_system = TRUE).
-- These are immutable templates; tenants may clone them into custom roles.
--
-- System keys: cashier | shift_manager | driver | kitchen | bar | manager
--
-- Permission model:
--   role_permissions  → controller-level (resource + action, default deny)
--   role_field_policies → field-level visibility (presence = visible, absence = hidden)
--
-- Manager role uses wildcard '*' to grant unrestricted access.
-- Runs as app_migrator (bypasses RLS) — safe because tenant_id IS NULL throughout.

-- ============================================================
-- 1. Roles
-- ============================================================
INSERT INTO roles (id, tenant_id, branch_id, name, system_key, is_system) VALUES
    ('00000001-0000-0000-0000-000000000001', NULL, NULL, 'Kasiyer',       'cashier',       TRUE),
    ('00000001-0000-0000-0000-000000000002', NULL, NULL, 'Shift Müdürü',  'shift_manager', TRUE),
    ('00000001-0000-0000-0000-000000000003', NULL, NULL, 'Şoför',         'driver',        TRUE),
    ('00000001-0000-0000-0000-000000000004', NULL, NULL, 'Mutfak',        'kitchen',       TRUE),
    ('00000001-0000-0000-0000-000000000005', NULL, NULL, 'Bar',           'bar',           TRUE),
    ('00000001-0000-0000-0000-000000000006', NULL, NULL, 'Yönetici',      'manager',       TRUE)
ON CONFLICT (system_key) DO NOTHING;

-- ============================================================
-- 2. role_permissions  (controller-level)
-- ============================================================

-- Kasiyer: adisyon okuma/açma/güncelleme, sipariş, masa okuma, katalog, ödeme oluşturma
INSERT INTO role_permissions (role_id, tenant_id, resource, action) VALUES
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks',   'read'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks',   'create'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks',   'update'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'orders',   'read'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'orders',   'create'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'orders',   'update'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'tables',   'read'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'catalog',  'read'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'payment',  'create'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'shifts',   'read')
ON CONFLICT DO NOTHING;

-- Shift Müdürü: kasiyer izinleri + silme/onay/vardiya yönetimi/personel okuma/rapor okuma
INSERT INTO role_permissions (role_id, tenant_id, resource, action) VALUES
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks',   'read'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks',   'create'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks',   'update'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks',   'delete'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks',   'approve'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'orders',   'read'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'orders',   'create'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'orders',   'update'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'orders',   'delete'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'orders',   'approve'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'tables',   'read'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'tables',   'create'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'tables',   'update'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'tables',   'delete'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'catalog',  'read'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'payment',  'read'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'payment',  'create'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'shifts',   'read'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'shifts',   'create'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'shifts',   'update'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'staff',    'read'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'reports',  'read')
ON CONFLICT DO NOTHING;

-- Şoför: yalnızca atanan siparişleri okuma ve teslimat durumu güncelleme
INSERT INTO role_permissions (role_id, tenant_id, resource, action) VALUES
    ('00000001-0000-0000-0000-000000000003', NULL, 'orders', 'read'),
    ('00000001-0000-0000-0000-000000000003', NULL, 'orders', 'update')
ON CONFLICT DO NOTHING;

-- Mutfak: sipariş takibi, katalog ve stok okuma
INSERT INTO role_permissions (role_id, tenant_id, resource, action) VALUES
    ('00000001-0000-0000-0000-000000000004', NULL, 'orders',    'read'),
    ('00000001-0000-0000-0000-000000000004', NULL, 'orders',    'update'),
    ('00000001-0000-0000-0000-000000000004', NULL, 'catalog',   'read'),
    ('00000001-0000-0000-0000-000000000004', NULL, 'inventory', 'read')
ON CONFLICT DO NOTHING;

-- Bar: mutfak ile aynı yetki seti
INSERT INTO role_permissions (role_id, tenant_id, resource, action) VALUES
    ('00000001-0000-0000-0000-000000000005', NULL, 'orders',    'read'),
    ('00000001-0000-0000-0000-000000000005', NULL, 'orders',    'update'),
    ('00000001-0000-0000-0000-000000000005', NULL, 'catalog',   'read'),
    ('00000001-0000-0000-0000-000000000005', NULL, 'inventory', 'read')
ON CONFLICT DO NOTHING;

-- Yönetici: wildcard — tüm kaynak ve eylemler
INSERT INTO role_permissions (role_id, tenant_id, resource, action) VALUES
    ('00000001-0000-0000-0000-000000000006', NULL, '*', '*')
ON CONFLICT DO NOTHING;

-- ============================================================
-- 3. role_field_policies  (field-level, default deny, presence = visible)
-- ============================================================

-- Kasiyer — görünür checks alanları (finansal özet görür, maliyet görmez)
INSERT INTO role_field_policies (role_id, tenant_id, resource, field) VALUES
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks', 'id'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks', 'status'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks', 'table_id'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks', 'opened_at'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks', 'closed_at'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks', 'cover_count'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks', 'note'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks', 'gross_total'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks', 'tax_total'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks', 'net_total'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'checks', 'discount_amount'),
    -- orders: temel alanlar
    ('00000001-0000-0000-0000-000000000001', NULL, 'orders', 'id'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'orders', 'status'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'orders', 'check_id'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'orders', 'items'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'orders', 'created_at'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'orders', 'note')
ON CONFLICT DO NOTHING;

-- Shift Müdürü — kasiyer alanları + raporlar/personel özeti
INSERT INTO role_field_policies (role_id, tenant_id, resource, field) VALUES
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks', 'id'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks', 'status'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks', 'table_id'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks', 'opened_at'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks', 'closed_at'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks', 'cover_count'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks', 'note'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks', 'gross_total'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks', 'tax_total'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks', 'net_total'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'checks', 'discount_amount'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'orders', 'id'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'orders', 'status'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'orders', 'check_id'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'orders', 'items'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'orders', 'created_at'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'orders', 'note'),
    -- raporlar: günlük özet (maliyet detayı yok)
    ('00000001-0000-0000-0000-000000000002', NULL, 'reports', 'daily_sales_count'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'reports', 'daily_sales_total'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'reports', 'payment_breakdown'),
    -- personel: temel bilgiler
    ('00000001-0000-0000-0000-000000000002', NULL, 'staff', 'id'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'staff', 'full_name'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'staff', 'email'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'staff', 'phone'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'staff', 'status')
ON CONFLICT DO NOTHING;

-- Şoför — sadece teslimat alanları
INSERT INTO role_field_policies (role_id, tenant_id, resource, field) VALUES
    ('00000001-0000-0000-0000-000000000003', NULL, 'orders', 'id'),
    ('00000001-0000-0000-0000-000000000003', NULL, 'orders', 'status'),
    ('00000001-0000-0000-0000-000000000003', NULL, 'orders', 'delivery_address'),
    ('00000001-0000-0000-0000-000000000003', NULL, 'orders', 'items'),
    ('00000001-0000-0000-0000-000000000003', NULL, 'orders', 'note')
ON CONFLICT DO NOTHING;

-- Mutfak — sipariş hazırlama alanları (ödeme/fiyat yok)
INSERT INTO role_field_policies (role_id, tenant_id, resource, field) VALUES
    ('00000001-0000-0000-0000-000000000004', NULL, 'orders', 'id'),
    ('00000001-0000-0000-0000-000000000004', NULL, 'orders', 'status'),
    ('00000001-0000-0000-0000-000000000004', NULL, 'orders', 'items'),
    ('00000001-0000-0000-0000-000000000004', NULL, 'orders', 'created_at'),
    ('00000001-0000-0000-0000-000000000004', NULL, 'orders', 'table_id'),
    ('00000001-0000-0000-0000-000000000004', NULL, 'orders', 'note')
ON CONFLICT DO NOTHING;

-- Bar — mutfak ile aynı
INSERT INTO role_field_policies (role_id, tenant_id, resource, field) VALUES
    ('00000001-0000-0000-0000-000000000005', NULL, 'orders', 'id'),
    ('00000001-0000-0000-0000-000000000005', NULL, 'orders', 'status'),
    ('00000001-0000-0000-0000-000000000005', NULL, 'orders', 'items'),
    ('00000001-0000-0000-0000-000000000005', NULL, 'orders', 'created_at'),
    ('00000001-0000-0000-0000-000000000005', NULL, 'orders', 'table_id'),
    ('00000001-0000-0000-0000-000000000005', NULL, 'orders', 'note')
ON CONFLICT DO NOTHING;

-- Yönetici: wildcard — tüm kaynaklar, tüm alanlar
INSERT INTO role_field_policies (role_id, tenant_id, resource, field) VALUES
    ('00000001-0000-0000-0000-000000000006', NULL, '*', '*')
ON CONFLICT DO NOTHING;
