# onlinemenu.b2b Denetiminden Aktarılan Dersler

> Tarih: 2026-07-02 · Kaynak: kardeş repo `onlinemenu.b2b`'nin tam kod denetimi (backend + frontend + wails).
> Bu doküman, orada tekrar tekrar regres eden güvenlik sınıflarının bu repoda **hiç doğmamasını** garanti altına almak için yazıldı. Buradaki maddeler ADR'lerle çelişmez; ADR'lerin **fiilen zorlandığını doğrulama** katmanıdır.

## Bağlam: b2b'de ne oldu?

b2b'de beş güvenlik bulgusu (şubenin `cost_price_tl` görmesi, cross-tenant item yazma, status makinesi yokluğu, `uuid.Nil` tenant bypass'ı, shipped sonrası tartım) defalarca uyarıya rağmen tekrar regres etti. Kök neden analizi tek bir sonuca vardı:

**Kuralların hepsi vardı — prose olarak.** CLAUDE.md'de yazıyordu, hatta kod olarak bile mevcuttu:
- Casbin RBAC init ediliyor, policy'ler seed'leniyordu → ama `RequirePermission` middleware'i **0 route'a** bağlıydı.
- Doğru tenant-scope soyutlaması (`scopes.ForTenant`) yazılmıştı → **kullanım sayısı 0**, ölü kod.
- Finansal alan gizleme fonksiyonları vardı → ama opt-in'di; her handler'da elle çağrılması gerekiyordu ve yeni modüller çağırmayı bıraktı.
- Bir test, `uuid.Nil` → "filtre yok" bypass'ını **doğru davranış olarak assert ediyordu.**

Çıkarılan ders: **"Kural var" ile "kural zorlanıyor" arasındaki farkı sadece CI'daki test/lint kapatır.** Varsayılan yol güvensizse (serialize et, query at, status ata), uyarı ne kadar tekrarlanırsa tekrarlansın N'inci call site'ta unutulur.

Bu repo mimari olarak b2b'nin derslerini zaten içeriyor (RLS + `WithTenantTx`, 4 katmanlı authz, DTO projection, go-arch-lint, contracts/). Aşağıdaki iş listesi, bu kuralların **kağıtta kalmadığını sürekli kanıtlayan** mekanizmaları ekler.

---

## Yapılacaklar (Opus için iş listesi)

### 1. "Wiring audit" — her ADR kuralının fiilen bağlı olduğunu doğrula

b2b'nin Casbin dersi: init edilen ≠ enforce edilen. Her iddia için kanıt üret:

- [ ] **RLS:** Her tenant tablosunda `FORCE ROW LEVEL SECURITY` migration'da var mı? `app_runtime` rolüyle bağlanan bir testcontainers testi, `SET LOCAL app.tenant_id` olmadan sorgunun **0 satır** döndürdüğünü assert etsin (ADR-SEC-001/002'nin canlı kanıtı).
- [ ] **`WithTenantTx` tekeli:** `pool.Query`/`pool.Exec`'in modül kodunda yasak olduğu söyleniyor — bunu go-arch-lint veya forbidigo kuralı olarak CI'da **fiilen kır**. Kural dosyada tanımlıysa, kasıtlı bir ihlal commit'inin CI'ı kırdığını bir kez doğrula.
- [ ] **OPA:** Embedded OPA gerçekten route middleware zincirinde mi, yoksa sadece init mi ediliyor? Her modülün route kaydında authz middleware varlığını assert eden bir test (route tablosunu iterate eden smoke test) ekle.
- [ ] **DTO projection:** Handler'ların domain/sqlc struct'ı doğrudan serialize etmediğini zorla — `json.Marshal`/`render` çağrılarında `modules/<x>/internal` tiplerini yasaklayan lint kuralı. b2b'de sızıntının kökü tam buydu.

### 2. Invariant regression testleri (CI'da zorunlu, tenant-critical path'lerde)

- [ ] **Alan yokluğu, sıfır değeri değil:** Kısıtlı rol için gizli alanların cevapta **JSON anahtarı olarak hiç bulunmadığını** assert et. b2b tuzağı: `decimal.Decimal` bir struct olduğu için `omitempty` çalışmıyordu — "gizlenen" maliyet `"0"` olarak gidiyordu. Pointer/ayrı DTO tipi kullan; testi `strings.Contains(body, "\"cost_price\"")` düzeyinde yaz.
- [ ] **Cross-tenant yazma denemesi:** Tenant A token'ı + Tenant B kaynak UUID'i → 404 (403 değil; varlık sızıntısı olmasın). Özellikle **child entity ID'siyle yapılan mutasyonlar** (b2b'deki delik: order item ID'siyle tenant'sız update).
- [ ] **Status geçişleri:** Adisyon/sipariş/ödeme durum makinelerinde geriye ve çapraz geçişlerin reddedildiği tablo-bazlı test. Geçiş kuralları tek bir `allowedTransitions` map'inde yaşasın; her mutasyon tek `Transition()` fonksiyonundan geçsin. b2b'de status ~10 dağınık noktada elle atanıyordu.
- [ ] **Sentinel yasağı:** "Boş/nil tenant = filtre yok" benzeri hiçbir magic değer olmasın. Platform-admin erişimi explicit, ayrı, adlandırılmış bir yol olsun (`AllTenants` suffix'li metot + ayrı authz kontrolü). b2b'de `uuid.Nil` ambient god-mode'du ve public endpoint'ler ona düşüyordu.
- [ ] **Public/cihaz endpoint'leri:** QR, device-pairing (ADR-SEC-004), webhook gibi auth'suz yüzeyler kendi **dar, alan-kısıtlı** query yollarını kullansın; genel repo metotlarına asla düşmesin.

### 3. Yeni modül şablonu

b2b'de en çarpıcı bulgu: yeni (daha temiz) modüller güvenlik disiplinini **taşımadı** — çünkü disiplin şablonda değil, hafızadaydı.

- [ ] Modül iskeleti üreten bir `task scaffold-module` (veya şablon dizini) hazırla: DTO projection, `WithTenantTx`, OPA kaydı, invariant test dosyası **hazır gelsin**. Yeni modülün güvenli olması için hatırlamak değil, silmek gerekmeli.

### 4. Frontend / sözleşme

- [ ] `contracts/openapi/` var — TS tiplerini **codegen ile** üret ve elle tip yazımını lint'le engelle. b2b'de 940 satır elle yazılmış tip, backend'le sözleşmesiz drift etmişti (branch'e hiç gelmeyen alan non-optional tanımlıydı).
- [ ] Rol/permission kontrolleri tek helper'da toplansın; component'lerde inline `role === 'admin'` yasak (lint veya review kuralı).
- [ ] i18n ilk günden: hardcoded string'leri yakalayan bir lint (i18next/no-literal-string) aç. b2b'de EN çevirisi fiilen yoktu ve toast'lar hardcoded Türkçe'ydi.

### 5. Wails POS istemcisi (b2b istasyon istemcisinin dersleri)

- [ ] `OpenInspectorOnStartup` / devtools yalnızca dev build'de — release'te kapalı olduğunu build config'de sabitle.
- [ ] Donanım okuma döngüleri (terazi, yazıcı, fiscal cihaz) hatayı **yutmasın**: bağlantı kopunca UI'a explicit `error`/`disconnected` event'i gitsin. b2b'de kopan terazi sonsuza dek "bağlı" görünüyordu.
- [ ] Baskı/fiscal komut hataları raporlansın — b2b'de başarısız fatura baskısı `//nolint:errcheck` yüzünden başarılı görünüyordu. Fiscal tarafında (ADR-FISCAL-001) bu sessiz başarı **yasal risk** olur.
- [ ] **Tek token-refresh yolu:** Go client ile webview axios'un ikisi birden refresh yapmasın; tek otorite (Go) + diğeri delege. b2b'de iki bağımsız interceptor aynı config dosyasına yarışıyordu.
- [ ] Token'ları plaintext config'e değil OS keychain'e (veya en azından 0600 + şifreli) yaz.

### 6. Süreç kuralı (asıl meta-ders)

- [ ] Bir güvenlik/mimari kuralı eklendiğinde PR şu ikisinden birini içermeden merge edilmesin: **(a)** kuralı zorlayan lint/arch-check, **(b)** ihlalini yakalayan regression testi. "CLAUDE.md'ye yazıldı" tek başına bir enforcement değildir — b2b bunun kanıtı.

---

*Kaynak denetim raporları: `onlinemenu.b2b/docs/CODE_AUDIT.md` ve `onlinemenu.b2b/docs/REFACTOR_ROADMAP.md`.*
