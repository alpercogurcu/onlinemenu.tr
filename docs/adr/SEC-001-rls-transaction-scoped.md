# ADR-SEC-001: RLS İçin Transaction-Scoped Tenant İzolasyonu

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-1
**Kategori:** Güvenlik (SEC)

## Bağlam

Multi-tenant POS platformunda shared schema + PostgreSQL Row-Level Security (RLS) kullanıyoruz. RLS policy'leri hangi tenant'a ait satırları göstereceğine karar vermek için bir session variable okuyacak. Bu variable'ı her request için doğru şekilde set etmek ve request bittiğinde temizlemek kritik — aksi halde bir request'in tenant context'i başka request'e sızar ve tüm izolasyon çöker.

Baseline dokümanda (`docs/architecture.md`) şu ifade bulunuyordu:

> "Transaction mode (default) çalışmaz — `SET LOCAL` transaction kapanınca sıfırlanır."

Bu ifade **teknik olarak hatalıdır** ve AI implementasyon kararlarını yanlış yönlendirir. `SET LOCAL` tam olarak transaction scope'a bağlıdır — sıfırlanması istenen davranıştır, bug değildir. pgBouncer transaction mode ile `SET LOCAL` mükemmel uyumludur.

## Karar

1. **Her HTTP request bir transaction içinde çalışır.** Transaction başında `SET LOCAL app.tenant_id = '<uuid>'` çalıştırılır.

2. **RLS policy'leri `current_setting('app.tenant_id', false)::uuid`** ile tenant'ı okur. İkinci argüman `false` olduğu için variable set edilmemişse PostgreSQL exception fırlatır — sessiz sızıntı yerine gürültülü hata.

3. **pgBouncer transaction mode** kullanılır (Faz 2+). Faz 0-1'de pgBouncer yok, pgxpool doğrudan.

4. **pgx exec mode:** Transaction mode ile uyumluluk için `pgxpool.Config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec` kullanılır.

5. **Platform helper:** `internal/platform/db/tenant_tx.go` içinde tek izinli desen:
   ```go
   func (p *Pool) WithTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error
   ```

6. **Yasak:** `SET` komutu (LOCAL olmadan) uygulama kodunda kullanılmaz — session leak riski. golangci-lint custom rule veya `depguard` ile yasaklanır.

7. **Yasak:** `pool.Query`, `pool.Exec` gibi doğrudan çağrılar modül kodunda kullanılmaz — yalnızca `WithTenantTx` üzerinden. Lint ile zorlanır.

## Sonuçlar

**İyi:**
- Transaction bittiğinde tenant context otomatik temizlenir; cross-request sızıntı yapısal olarak imkânsız.
- pgBouncer transaction mode kullanılabilir; connection pooling verimi korunur.
- RLS variable set edilmeden query çalışırsa PostgreSQL exception atar; bug erken görünür.
- Her DB çağrısı zaten transaction'da olduğu için ek runtime maliyet minimum.

**Dikkat:**
- Geliştiricinin `WithTenantTx` dışında DB çağrısı yapma olasılığı var. Lint + code review + RLS sızıntı testi bunu yakalar.
- pgx exec mode değişikliği statement cache optimization'ı devre dışı bırakır. Pratik performansa etkisi ihmal edilebilir.

**Risk:**
- `SET LOCAL` değil `SET` kullanımı yasak — lint kuralı ve PR review şart.

## Değerlendirilen Alternatifler

- **pgBouncer session mode:** Reddedildi. Connection pooling faydası büyük ölçüde kaybolur.
- **Database-per-tenant:** Reddedildi. Binlerce tenant'ta operasyonel cehennem.
- **Schema-per-tenant:** Reddedildi. Migration'ı her schema'da tekrar çalıştırmak gerekir.
- **Uygulama katmanında filter (RLS yok):** Reddedildi. Tek unutulan `WHERE tenant_id = ?` = sızıntı.
