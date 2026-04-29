# ADR-SEC-002: RLS FORCE ve Ayrı Runtime Rolü

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-2
**Kategori:** Güvenlik (SEC)

## Bağlam

Baseline'daki RLS örneği yalnızca `ENABLE ROW LEVEL SECURITY` kullanıyordu. Bu **yetersizdir**: PostgreSQL'de tablo sahibi (owner) RLS'yi default olarak **bypass eder**. Eğer uygulama bağlantısı migration'ı çalıştıran rol ile aynıysa, RLS sessizce etkisiz kalır ve tüm tenant izolasyonu illüzyondan ibaret olur.

Ayrıca baseline'da RLS policy'sinin `WITH CHECK` kısmı her yerde tutarlı yazılmamıştı; INSERT'te yanlış tenant_id ile satır yazılabilmesi riski vardı.

## Karar

1. **İki ayrı PostgreSQL rolü:**

   ```sql
   -- Migration rolü: tablo yaratır, policy tanımlar. Tablo sahibi (OWNER) bu rol olur.
   CREATE ROLE app_migrator LOGIN PASSWORD '...';

   -- Uygulama rolü: RUNTIME bağlantıları yalnızca bu rolle yapılır. RLS zorunludur.
   CREATE ROLE app_runtime LOGIN PASSWORD '...';

   GRANT USAGE ON SCHEMA public TO app_runtime;
   GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO app_runtime;
   ```

2. **Her RLS tablosunda FORCE zorunlu:**

   ```sql
   ALTER TABLE <table> ENABLE ROW LEVEL SECURITY;
   ALTER TABLE <table> FORCE ROW LEVEL SECURITY;  -- Owner bile bypass edemez
   ```

3. **Policy şablonu (hem USING hem WITH CHECK):**

   ```sql
   CREATE POLICY tenant_read ON <table>
       FOR SELECT USING (tenant_id = current_setting('app.tenant_id', false)::uuid);

   CREATE POLICY tenant_write ON <table>
       FOR ALL USING (tenant_id = current_setting('app.tenant_id', false)::uuid)
                 WITH CHECK (tenant_id = current_setting('app.tenant_id', false)::uuid);
   ```

4. **Migration şablonu zorunluluğu:** Yeni tenant-scoped tablo oluşturan her migration `ENABLE + FORCE ROW LEVEL SECURITY` içermek zorunda. CI lint (`scripts/lint_rls.sh`) `pg_catalog` sorgularıyla hangi tabloda bu iki ayar eksik ise CI fail eder.

5. **Runtime bağlantı konfigürasyonu:**
   - `DATABASE_URL` env variable → `app_runtime` rolü
   - `MIGRATION_DATABASE_URL` (ayrı) → `app_migrator` rolü
   - Migration CLI hariç hiçbir binary migrator rolüyle bağlanmaz.

## Sonuçlar

**İyi:**
- Owner ayrıcalığı ile RLS bypass edilmesi yapısal olarak imkânsız.
- INSERT/UPDATE ile yanlış tenant_id'ye veri yazma policy'i engeller.
- Her iki rol minimum ayrıcalıkla sınırlı — least privilege.

**Dikkat:**
- İki rol yönetmek operasyonel küçük yük (credential rotation iki kat). Vault bunu otomatikleştirir.
- Migration'ı runtime ile koşma cazibesi var (dev ortamında). Docker Compose'da bile iki ayrı rol tanımlanmalı.

**Risk:**
- Yeni eklenen tablolarda RLS unutulursa sızıntı. CI lint tek savunma hattı; ihmal etme.

## Değerlendirilen Alternatifler

- **Tek rol + güvenim var mantığı:** Reddedildi. Migration owner ile gelir, sonra runtime'da dikkat ederiz — human error'a açık.
- **RLS yerine application-layer filter:** Reddedildi (SEC-001'deki gerekçeler).
- **SECURITY DEFINER fonksiyonlar:** Reddedildi. sqlc + pgx avantajlarını kaybettirir.
