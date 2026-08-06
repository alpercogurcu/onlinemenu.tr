# Yedekleme ve Restore — ADR-OPS-001

Bu klasör [ADR-OPS-001](../../docs/adr/OPS-001-backup-dr.md)'in "cluster-level backup" kararının
fiili implementasyonudur: `deploy/docker-compose.prod.yml`'deki `postgres-backup` servisi bu
scriptleri çalıştırır.

## Kapsam ve bilinçli sınır

ADR yalnızca Postgres'ten bahsediyor (MinIO/nesne depolama yedeği ADR'de **yok**). Bu implementasyon
de yalnızca Postgres'i kapsıyor — ek olarak: `backend/internal/platform/storage/storage.go` şu an
yalnızca bir arayüz, somut MinIO implementasyonu henüz **yazılmamış** (`minio.go` yok). Yani bugün
MinIO'dan geçen hiçbir veri yok; yedeklenecek bir şey de yok. Somut implementasyon geldiğinde bu
karar yeniden değerlendirilmeli.

## Ne yedekleniyor

Tek bir Postgres cluster'ında **iki veritabanı** var (bkz. `deploy/postgres/init.sql`):

| Veritabanı | İçerik | Sahip rol |
|---|---|---|
| `onlinemenu` (prod) / `onlinemenu_dev` (dev) | Uygulama şeması, tüm modüller | `app_migrator` (BYPASSRLS) |
| `keycloak` | Kullanıcılar, kimlik bilgileri, realm config | `postgres` (superuser) |

Artı **cluster globals**: `app_migrator`/`app_runtime` rolleri ve grant'leri veritabanı içinde değil,
cluster seviyesinde yaşar (`pg_dumpall --globals-only`). Bunlar olmadan restore edilen bir dump,
sahiplik/grant hatalarıyla bozuk gelir.

`backup.sh` üçünü de tek çalışmada üretir: `<db>.dump` (custom format, `pg_restore` ile seçici geri
yükleme) ve `globals.sql`.

## Neden `app_migrator` veya `app_runtime` DEĞİL, cluster superuser

Bu, testte gerçekten yakalanan iki gerçek hata:

1. **`app_runtime` ile dump = sessiz veri kaybı.** ADR-SEC-002'nin `FORCE ROW LEVEL SECURITY`'si
   `app_runtime`'ı bypass ettirmez; rolün `app.tenant_id` set edilmeden gördüğü satır sayısı sıfırdır.
   `pg_dump` bu durumda **hata vermez** — exit code 0, boş ama "başarılı" bir dump üretir. Script bu
   yüzden dump almadan önce bilinen bir tabloda satır sayısı doğrular (`BACKUP_SANITY_TABLE`,
   varsayılan `tenants`) ve sıfırsa **abort** eder.
2. **`app_migrator` ile `keycloak` DB'sini dump'lamak izin hatası verir.** `keycloak` veritabanı
   `postgres` rolüne ait (`init.sql`: `GRANT ALL PRIVILEGES ON DATABASE keycloak TO postgres`).
   `app_migrator`'ın `BYPASSRLS`'i var ama `keycloak`'ın kendi tablolarında yetkisi yok — denendi,
   `permission denied for table databasechangeloglock` ile patladı.

Tek çözüm: **her iki veritabanını da okuyabilen tek rol** — cluster superuser (`postgres`).
`BACKUP_SUPERUSER` env değişkeni bunu bekler.

## Zamanlama

**Tek zamanlayıcı: compose içi sidecar döngüsü** (host cron değil — ikisini birden kurmadık).
`entrypoint.sh` `backup.sh`'i `BACKUP_INTERVAL_SECONDS` aralıklarla çalıştıran sonsuz bir döngü.
Varsayılan: **21600 saniye (6 saat)**.

**RPO/RTO — ADR'nin hedefine karşı fiilen ne elde ediliyor:**
- ADR hedefi: RPO < 15 dakika, RTO < 1 saat (pgBackRest/WAL-G sürekli WAL shipping ile).
- Bu implementasyon: **RPO ≈ 6 saat** (backup aralığı = veri kaybı üst sınırı), **RTO ≈ dakikalar**
  (küçük, tek-tenant veritabanı; test restoresü ~2 saniye sürdü).
- Bu bir ADR ihlali değil — ADR'nin kendi "İmplementasyon Detayları (Dolacak)" bölümü pgBackRest/
  WAL-G kararını **"Faz 2 başı"na** erteliyor. Mantıksal dump + `BACKUP_INTERVAL_SECONDS` bugünün
  ölçeği (tek şube, tek kasa) için kabul edilebilir bir ara adım; sürekli WAL shipping'e geçiş Faz 2.
- Aralığı düşürmek ucuz: tek-tenant dump ~1 saniyede bitiyor (test: 772KB). `BACKUP_INTERVAL_SECONDS`
  daha sık istenirse (ör. 3600) doğrudan düşürülebilir.

## Saklama (retention)

`RETENTION_DAYS` (varsayılan **14 gün**) — yalnızca **lokal** dump klasörlerini kapsar
(`find $BACKUP_DIR -mtime +$RETENTION_DAYS`). S3 hedefine yüklenmişse, o bucket'ın kendi lifecycle
kuralı (varsa) ayrı bir karar — bu script uzak silme yapmaz.

**Bu, soft-delete penceresiyle (30 gün genel / 10 yıl mali kayıt, ADR-OPS-001 madde 5) KARIŞTIRILMAMALI.**
Soft-delete = uygulama seviyesinde `deleted_at` ile "tenant kendi verisini ne kadar geri
alabilir". Backup retention = "felaket durumunda kaç günlük dump saklıyoruz". İkisi bağımsız.

## Hedef: yerel varsayılan, S3-uyumlu opsiyonel — sağlayıcı kilidi yok

Barındırma sağlayıcısı henüz seçilmedi. Bu yüzden:

- **Varsayılan: yalnız yerel.** `BACKUP_DIR` (varsayılan `/backups`, named volume) — sağlayıcı
  kararı gerektirmez.
- **Opsiyonel: herhangi bir S3-uyumlu hedef.** `BACKUP_S3_ENDPOINT` + `BACKUP_S3_BUCKET` +
  `BACKUP_S3_ACCESS_KEY` + `BACKUP_S3_SECRET_KEY` set edilirse `mc` (MinIO Client) ile yüklenir.
  `mc` MinIO'ya özel değil — herhangi bir S3-uyumlu endpoint'i (AWS S3, Backblaze B2, Wasabi,
  Cloudflare R2, self-hosted MinIO...) `mc alias set` ile hedefleyebilir. Sağlayıcı SDK'sı yok,
  kilitlenme yok. Env değişkenleri boşsa upload adımı atlanır — hata değil.
- `mc` binary'si pinlenmiş bir sürümden (`RELEASE.2025-08-13T08-35-41Z`) sha256 doğrulamasıyla
  indirilir ve bir named volume'e (`backup-tools-data`) yazılır — restart'ta tekrar indirmez.

## Sırlar — üçüncü mekanizma yok

`BACKUP_SUPERUSER_PASSWORD` ve (varsa) `BACKUP_S3_ACCESS_KEY`/`BACKUP_S3_SECRET_KEY` bu repo'nun
zaten kullandığı mekanizmayla gelir: **bootstrap sınıfı sırlar → `.env.sops`** (Postgres superuser
şifresi zaten `docker-compose.prod.yml`'nin kendisinde bootstrap değişkeni; ayrı bir Vault okuması
eklemedik çünkü backup sidecar'ı fx/Vault client'ını paylaşmıyor — bağımsız bir container). Yeni bir
sır deposu **icat edilmedi**.

## Test edilen restore

Bu script gerçekten çalıştırıldı, sadece yazıldı değil:

1. `backup.sh`, **dev ortamındaki** `onlinemenu-dev-postgres-1` konteynerine salt-okunur bağlandı
   (`pg_dump`/`pg_dumpall` — hiçbir INSERT/UPDATE/DELETE yok, `migrate up/down` çalıştırılmadı,
   dev konteyneri hiç durdurulmadı).
2. Dump, **atılabilir** (throwaway) bir `pgvector/pgvector:pg17` konteynerine `restore.sh` ile
   geri yüklendi.
3. Restore sonrası doğrulandı: `tenants` 1 satır, `branches` 1 satır, `persons` 51 satır,
   `keycloak.user_entity` 2 satır — **restore çalışıyor ve gerçek veri geri geliyor**, yalnızca
   "komut 0 döndürdü" değil.
4. Atılabilir konteynerler test sonunda silindi; dev ortamına hiç yazılmadı.

### Restore prosedürü (özet — tam script: `restore.sh`)

```bash
# 1. Hedef Postgres'i KAYNAK ile AYNI imajla başlat (pgvector/pgvector:pg17 — sade
#    postgres imajıyla restore denendi, "extension vector is not available" ile patladı).
#    POSTGRES_PASSWORD'u kaynağın postgres şifresiyle AYNI ver — aşağıdaki not'a bak.
docker run -d --name restore-target -e POSTGRES_PASSWORD=<kaynağın postgres şifresi> pgvector/pgvector:pg17

# 2. restore.sh'i dump klasörüne işaret ederek çalıştır
docker run --rm --network <hedefin ağı> \
  -e PGHOST=restore-target -e BACKUP_SUPERUSER=postgres \
  -e BACKUP_SUPERUSER_PASSWORD=<kaynağın postgres şifresi> \
  -e APP_DB=onlinemenu -e KEYCLOAK_DB=keycloak \
  -e DUMP_DIR=/backups/<zaman-damgası> \
  -v $(pwd)/deploy/backup/restore.sh:/scripts/restore.sh:ro \
  -v <backup-dir>:/backups:ro \
  postgres:17-alpine sh /scripts/restore.sh
```

**Önemli tuzak (testte gerçekten yaşandı):** `globals.sql` restore edilirken, kaynakta var olan
her rolün (özellikle **`postgres`'in kendisinin**) şifresi `ALTER ROLE ... PASSWORD` ile kaynaktaki
değere **üzerine yazılır**. Hedef konteyneri farklı bir `POSTGRES_PASSWORD` ile başlatırsanız,
globals restore'undan SONRAKİ bağlantı **auth hatasıyla düşer** (birebir test edildi). Gerçek bir
DR senaryosunda operatör kaynağın şifresini zaten Vault/`.env.sops`'tan biliyor olmalı — hedefi
baştan o şifreyle başlatın.

`role "postgres" already exists` hatası **beklenen ve zararsızdır** (script `ON_ERROR_STOP=0` ile
devam eder) — yalnızca hedef, kaynağın kendi `init.sql`'iyle zaten bootstrap edilmiş bir compose
ortamıysa görülür; bomboş bir cluster'da görülmez.

## Genel kurtarma (bare cluster, init.sql yoksa)

`restore.sh` `globals.sql`'i uygulayıp veritabanlarını `CREATE DATABASE` ile açar — `init.sql`'in
rol/extension kurulumuna ihtiyaç duymaz, `pgvector`/`uuid-ossp`/`pg_trgm` extension'ları dump içinde
zaten `CREATE EXTENSION IF NOT EXISTS` olarak gelir (hedef imaj `pgvector/pgvector:pg17` olduğu
sürece). Tek gereksinim: hedef imajın kaynakla **aynı** olması (yukarıdaki tuzağa bakın).
