# Online Menu — Veritabanı Şeması

## Genel Prensipler

- Her tabloda `tenant_id UUID NOT NULL` zorunlu — RLS policy bu kolonu kullanır.
- Para değerleri `NUMERIC(18,4)` + `currency CHAR(3)` kolonu ile saklanır.
- Tüm tablolarda `created_at TIMESTAMPTZ NOT NULL DEFAULT now()` ve `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`.
- Append-only tablolar (`stock_movements`, `payments`, `outbox_events`, `inbox_events`) immutable — güncelleme/silme yasak.
- Soft delete için `deleted_at TIMESTAMPTZ` kullanılır; mali kayıtlarda (payment, invoice) hard delete yasaktır.
- UUID v7 (sıralı) `id` primary key olarak tercih edilir — index fragmentasyonu azalır.
- Migration'lar modül başına `migrations/<module>/` altında tutulur ve bağımsız çalıştırılır.

---

## ER Diyagramı

> **Not:** Diyagramda alan kalabalığını önlemek için `tenant_id`, `created_at`, `updated_at` tekrar gösterilmemiştir. Her tabloda mevcuttur.

```mermaid
erDiagram

    %% ──────────────────────────────
    %% TENANT & ŞUBE MODELİ
    %% ──────────────────────────────

    TENANTS ||--o{ BRANCHES : "has"
    TENANTS ||--o{ USERS : "has"
    BRANCHES ||--|| BRANCH_SETTINGS : "has"
    BRANCHES ||--o{ BRANCH_USERS : "employs"
    USERS ||--o{ BRANCH_USERS : "member_of"
    USERS ||--|| EMPLOYEE_PROFILES : "detailed_as"
    BRANCHES ||--o{ DEVICES : "owns"

    TENANTS {
        uuid id PK
        text name
        text slug
        text plan "starter|pro|enterprise"
        jsonb enabled_modules
        bool is_active
    }

    BRANCHES {
        uuid id PK
        uuid tenant_id FK
        text name
        text slug
        text ownership_type "sube|franchise"
        text operation_type "restoran|bar|market|food_truck|imalat|depo"
        jsonb supply_rules
        bool is_active
    }

    BRANCH_SETTINGS {
        uuid branch_id PK_FK
        text billing_provider "edm|parasut|mikro|logo"
        jsonb billing_config
        text currency "TRY"
        text timezone "Europe/Istanbul"
        interval business_day_cutoff "default:00:00:00 bar=05:00:00"
        text pos_terminal_type "ingenico|verifone|none"
        jsonb pos_config
        uuid default_price_list_id FK
        text fiscal_device_type "none|mock|hugin|profilo|beko|ingenico_yn|verifone_yn"
        jsonb fiscal_device_config
    }

    USERS {
        uuid id PK
        uuid tenant_id FK
        text keycloak_sub UK
        text email UK
        text full_name
        bool is_active
    }

    BRANCH_USERS {
        uuid branch_id FK
        uuid user_id FK
        text role "admin|manager|cashier|waiter|kitchen|warehouse"
        bool is_primary_branch
    }

    EMPLOYEE_PROFILES {
        uuid user_id PK_FK
        uuid tenant_id FK
        text tc_kimlik_hash
        date hire_date
        date termination_date
        jsonb contact_info
        jsonb emergency_contact
    }

    DEVICES {
        uuid id PK
        uuid branch_id FK
        text name
        text device_type "pos_terminal|kds|local_server|tablet"
        text fingerprint UK
        text fingerprint_method "tpm|keystore|machine-id"
        text firmware_version
        text status "active|inactive|blocked"
        text pairing_code_hash
        timestamptz pairing_expires_at
        timestamptz last_token_rotated_at
        timestamptz revoked_at
        text revoke_reason
        timestamptz last_seen_at
    }

    %% ──────────────────────────────
    %% KATALOG
    %% ──────────────────────────────

    CATEGORIES ||--o{ PRODUCTS : "groups"
    PRODUCTS ||--o{ PRODUCT_VARIANTS : "has"
    PRODUCTS ||--o{ MODIFIER_GROUPS : "has"
    MODIFIER_GROUPS ||--o{ MODIFIERS : "contains"
    PRICE_LISTS ||--o{ PRICE_LIST_ITEMS : "includes"
    PRODUCT_VARIANTS ||--o{ PRICE_LIST_ITEMS : "priced_in"
    BRANCHES ||--o{ BRANCH_MENUS : "offers"
    BRANCH_MENUS }o--|| PRICE_LISTS : "uses"

    CATEGORIES {
        uuid id PK
        uuid tenant_id FK
        uuid parent_id FK
        text name
        text slug
        text image_url
        int sort_order
        bool is_active
    }

    PRODUCTS {
        uuid id PK
        uuid tenant_id FK
        uuid category_id FK
        text sku UK
        text name
        text description
        text product_type "item|combo|service"
        text unit "adet|kg|lt|porsiyon"
        bool is_stock_tracked
        bool is_active
        jsonb attributes
    }

    PRODUCT_VARIANTS {
        uuid id PK
        uuid product_id FK
        text name
        text barcode UK
        numeric base_price
        text currency
        bool is_default
        bool is_active
    }

    MODIFIER_GROUPS {
        uuid id PK
        uuid product_id FK
        text name
        int min_select
        int max_select
        bool is_required
    }

    MODIFIERS {
        uuid id PK
        uuid group_id FK
        text name
        numeric extra_price
        text currency
        bool is_active
    }

    PRICE_LISTS {
        uuid id PK
        uuid tenant_id FK
        text name
        text currency
        date valid_from
        date valid_to
        bool is_active
    }

    PRICE_LIST_ITEMS {
        uuid price_list_id FK
        uuid variant_id FK
        numeric price
    }

    BRANCH_MENUS {
        uuid branch_id FK
        uuid price_list_id FK
        jsonb hidden_category_ids
        jsonb hidden_product_ids
        bool is_active
    }

    %% ──────────────────────────────
    %% POS — MASA / ADİSYON / SİPARİŞ
    %% ──────────────────────────────

    BRANCHES ||--o{ TABLE_ZONES : "has"
    TABLE_ZONES ||--o{ TABLES : "contains"
    TABLES ||--o{ CHECKS : "hosts"
    CHECKS ||--o{ ORDERS : "splits_into"
    ORDERS ||--o{ ORDER_ITEMS : "contains"
    ORDER_ITEMS ||--o{ ORDER_ITEM_MODIFIERS : "has"
    CHECKS ||--o{ PAYMENTS : "paid_by"
    USERS ||--o{ CHECKS : "opened_by"
    USERS ||--o{ ORDERS : "taken_by"

    TABLE_ZONES {
        uuid id PK
        uuid branch_id FK
        text name
        int floor
        bool is_active
    }

    TABLES {
        uuid id PK
        uuid branch_id FK
        uuid zone_id FK
        text name
        int capacity
        text status "empty|occupied|reserved|cleaning"
        jsonb layout_position
        bool is_active
    }

    CHECKS {
        uuid id PK
        uuid tenant_id FK
        uuid branch_id FK
        uuid table_id FK
        uuid opened_by_id FK
        uuid closed_by_id FK
        int pax
        text status "open|partial_paid|paid|void"
        numeric subtotal
        numeric discount_total
        numeric tax_total
        numeric grand_total
        text currency
        timestamptz opened_at
        timestamptz closed_at
        text note
    }

    ORDERS {
        uuid id PK
        uuid check_id FK
        uuid taken_by_id FK
        int seq
        text channel "pos|kds|online"
        text status "pending|preparing|served|cancelled"
        text note
        timestamptz placed_at
        timestamptz served_at
    }

    ORDER_ITEMS {
        uuid id PK
        uuid order_id FK
        uuid variant_id FK
        int quantity
        numeric unit_price
        text currency
        numeric line_total
        text note
        text status "pending|preparing|served|cancelled|voided"
    }

    ORDER_ITEM_MODIFIERS {
        uuid order_item_id FK
        uuid modifier_id FK
        text name
        numeric extra_price
        int quantity
    }

    PAYMENTS {
        uuid id PK
        uuid check_id FK
        uuid tenant_id FK
        text method "cash|card|transfer|virtual_pos"
        text terminal_id
        numeric amount
        numeric change_given
        text currency
        text status "pending|completed|refunded|failed"
        text provider_ref
        jsonb provider_response
        timestamptz paid_at
    }

    %% ──────────────────────────────
    %% STOK & DEPO
    %% ──────────────────────────────

    BRANCHES ||--o{ WAREHOUSES : "operates"
    WAREHOUSES ||--o{ STOCK_LEVELS : "stores"
    PRODUCTS ||--o{ STOCK_LEVELS : "tracked_as"
    WAREHOUSES ||--o{ STOCK_MOVEMENTS : "logs"
    SHIPMENTS ||--o{ SHIPMENT_ITEMS : "contains"
    SHIPMENTS }o--|| WAREHOUSES : "ships_from"
    SHIPMENTS }o--|| BRANCHES : "ships_to"

    WAREHOUSES {
        uuid id PK
        uuid tenant_id FK
        uuid branch_id FK
        text name
        text warehouse_type "depo|imalat"
        bool is_active
    }

    STOCK_LEVELS {
        uuid warehouse_id FK
        uuid product_id FK
        numeric on_hand
        numeric reserved
        numeric available
        numeric reorder_point
        text unit
    }

    STOCK_MOVEMENTS {
        uuid id PK
        uuid tenant_id FK
        uuid warehouse_id FK
        uuid product_id FK
        numeric quantity
        text movement_type "in|out|adjust|transfer|reserve|release"
        text reference_type "shipment|order|purchase_order|manual"
        uuid reference_id
        text note
        uuid created_by FK
        timestamptz occurred_at
    }

    SHIPMENTS {
        uuid id PK
        uuid tenant_id FK
        uuid from_warehouse_id FK
        uuid to_branch_id FK
        text status "draft|approved|in_transit|received|cancelled"
        text priority "normal|urgent"
        text note
        uuid created_by FK
        timestamptz shipped_at
        timestamptz received_at
    }

    SHIPMENT_ITEMS {
        uuid shipment_id FK
        uuid product_id FK
        numeric requested_qty
        numeric shipped_qty
        numeric received_qty
        text unit
    }

    %% ──────────────────────────────
    %% CARİ (PARTY)
    %% ──────────────────────────────

    SUPPLIERS ||--o{ PURCHASE_ORDERS : "issues"
    PURCHASE_ORDERS ||--o{ PURCHASE_ORDER_ITEMS : "contains"

    SUPPLIERS {
        uuid id PK
        uuid tenant_id FK
        text name
        text tax_office
        text tax_no UK
        jsonb contact
        jsonb bank_accounts
        bool is_active
    }

    CUSTOMERS {
        uuid id PK
        uuid tenant_id FK
        text name
        text tax_office
        text tax_no
        text efatura_alias
        text identity_type "vergi|tckn"
        jsonb contact
        bool is_active
    }

    PURCHASE_ORDERS {
        uuid id PK
        uuid tenant_id FK
        uuid branch_id FK
        uuid supplier_id FK
        text status "draft|sent|partial|received|cancelled"
        numeric subtotal
        numeric tax_total
        numeric grand_total
        text currency
        date expected_date
    }

    PURCHASE_ORDER_ITEMS {
        uuid purchase_order_id FK
        uuid product_id FK
        numeric quantity
        numeric unit_price
        text currency
        numeric received_qty
    }

    %% ──────────────────────────────
    %% BİLLİNG (E-FATURA)
    %% ──────────────────────────────

    CUSTOMERS ||--o{ INVOICES : "receives"
    CHECKS ||--o{ INVOICES : "produces"

    INVOICES {
        uuid id PK
        uuid tenant_id FK
        uuid branch_id FK
        uuid customer_id FK
        uuid check_id FK
        text invoice_type "satis|iade|irsaliye"
        text provider "edm|parasut|mikro|logo"
        text provider_ref
        text ettn
        text status "draft|queued|sent|accepted|rejected|cancelled"
        numeric subtotal
        numeric tax_total
        numeric grand_total
        text currency
        timestamptz issued_at
        timestamptz accepted_at
        jsonb provider_response
    }

    %% ──────────────────────────────
    %% SYNC (OUTBOX / INBOX)
    %% ──────────────────────────────

    OUTBOX_EVENTS {
        uuid id PK
        uuid tenant_id FK
        uuid branch_id FK
        text aggregate_type
        uuid aggregate_id
        text event_type
        int event_version
        jsonb payload
        text subject "NATS subject"
        bool is_synced
        bool is_dead
        int retry_count
        timestamptz next_retry_at
        text last_error
        timestamptz created_at
        timestamptz synced_at
    }

    INBOX_EVENTS {
        uuid id PK
        uuid tenant_id FK
        text source_device_id
        text event_type
        int event_version
        jsonb payload
        bool is_applied
        timestamptz received_at
        timestamptz applied_at
    }
```

> **Not:** Idempotency key'leri Redis'te saklanır (TTL 24 saat). DB tablosu yok — Faz 2'de denetim kaydı gerekliliği doğarsa eklenir (ADR-SEC-003).

---

## Önemli Index'ler

```sql
-- RLS performansı için zorunlu
CREATE INDEX ON orders (tenant_id);
CREATE INDEX ON checks (tenant_id, branch_id, status);
CREATE INDEX ON order_items (order_id);

-- Stok sorguları
CREATE INDEX ON stock_levels (warehouse_id, product_id);
CREATE INDEX ON stock_movements (warehouse_id, product_id, occurred_at DESC);

-- Sync queue
CREATE INDEX ON outbox_events (is_synced, created_at) WHERE is_synced = false;
CREATE INDEX ON outbox_events (tenant_id, branch_id, is_synced);

-- Fatura arama
CREATE INDEX ON invoices (tenant_id, branch_id, status, issued_at DESC);
CREATE INDEX ON invoices (provider_ref) WHERE provider_ref IS NOT NULL;
```

---

## Modül — Migration Eşlemesi

| Modül | Migration Dizini | Bağımlı Modüller |
|---|---|---|
| tenant | `migrations/tenant/` | — |
| identity | `migrations/identity/` | tenant |
| hr | `migrations/hr/` | tenant, identity |
| catalog | `migrations/catalog/` | tenant |
| pos | `migrations/pos/` | tenant, catalog |
| inventory | `migrations/inventory/` | tenant, catalog |
| party | `migrations/party/` | tenant |
| billing | `migrations/billing/` | tenant, party |
| payment | `migrations/payment/` | tenant, pos |
| edge_sync | `migrations/edge_sync/` | tenant |

Migration çalıştırma sırası yukarıdaki bağımlılık grafiğine göre belirlenir. CI'da `make migrate-up` tüm modülleri doğru sırayla uygular.
