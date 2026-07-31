# Pilot Takip Listesi — İlk Satışa Kadar

> Oluşturma: 2026-07-31 · Kapsam: **online-only pilot** (tek şube, sabit internet, edge-sync yok)
> Repo'da Jira yok; kalıcı kayıt burada tutulur. Bir madde tamamlanınca bu dosyadan silinir.
> Kardeş listeler: [backlog-fiscal.md](backlog-fiscal.md) · Referans: [lessons-from-odoo.md](lessons-from-odoo.md)

**Ürün kararı (2026-07-31):** offline-first ilk satışa girmiyor. Pilot müşteri sabit internetli,
tek şubeli bir işletme olacak. `edge-sync` Faz 2'ye kalır; ROADMAP Faz 1'in "satılabilir MVP"
tanımı bu pilottan sonra tamamlanır.

**Halihazırda çalışan:** satış omurgası uçtan uca test edilmiş
(`internal/e2e/spine_test.go`: adisyon aç → sipariş → ödeme → ÖKC mali kayıt → settle → kapat),
Token/Beko X30TR entegrasyonu gerçek, admin panelinde 29 sayfa, Wails POS istemcisi.

---

## Sıra ve bağımlılıklar

```
1 WIP yeşil ──► 2 onboarding ──► 3 kasa oturumu ──► 4 gün sonu raporu
                                        │
                                        └──► (izin sözlüğü: madde 7'nin testi önce)
5 deploy hedefi  ·  6 tenant test açığı  ·  7 izin wiring testi   → paralel, bağımsız
8 Token sertifikasyon → dış bağımlılık, takvimi bizde değil
```

Madde 4 madde 3'e **bağımlı**: gün sonu raporunun nakit satırı kasa oturumunun açılış bakiyesini
kullanır (`final_count = total + açılış + nakit_hareketi`). Paralel planlanamaz.

---

## 1. WIP'i yeşile çek ve commit'le

Çalışan ağaçta commit edilmemiş iş var ve derlenmiyor.

- [ ] `role_tenant_guard_test.go` eksik import'ları (`path/filepath`, `golang-migrate/migrate/v4`)
- [ ] `fiscalStatus.test.ts` bayat beklentisi — kod doğru (`fiscalStatus.ts:462-463` bilinçli yorum),
      test "başka istasyonda" bekliyor, kod "başka işlemde" üretiyor
- [x] ADR-SEC-005'e "000013 eki" bölümü yazıldı
- [ ] `task backend:lint` yeşil — arch-lint refactor'ünün bittiğini bu doğrular
- [ ] Alanlara göre gruplu commit (`main`, Jira kodsuz — kullanıcı onayı 2026-07-31)

Bittiğinde `backlog-fiscal.md`'den şu iki madde silinir: *"memberships.tenant_id ↔ roles.tenant_id"*
ve *"Custom rol API'sinde branch_scoped taşınmıyor"*.

## 2. Personel onboarding zinciri — kopuk

**Bu satışa çıkmayı tek başına engelliyor: bugün sisteme yeni kasiyer eklenemiyor.**

Doğrulanan durum:
- `persons` tablosuna INSERT eden tek yol `PersonService.Create`; yalnızca **devre dışı**
  `POST /persons`'tan çağrılıyor (`routes.go:64-67`, yorum satırı)
- İlk Keycloak girişinde otomatik provizyon **yok** — `ListContexts` → `GetByKeycloakSub` →
  `ErrNotFound` → hata. Yeni kullanıcı boş bağlam listesi değil, hata alıyor
- `hr-core.CreateEmployee` `person_id` şart koşuyor: *"The person must already exist in the
  identity module"*; membership de öyle

> **Not — önceki değerlendirme düzeltmesi:** bu maddeyi başta "kısıtsız cross-tenant yüzey"
> diye güvenlik açığı olarak işaretlemiştim. Yanlıştı; uçlar mount edilmiyor, açık delik yok.
> Sorun güvenlik değil, **eksik yol**.

Önerilen çözüm (uygulanmadan önce teyit):
- [ ] `/me/contexts` (pre-context yol) doğrulanmış Keycloak claim'lerinden person'ı **upsert etsin**.
      Person satırı tek başına hiçbir yetki vermez — yetki `memberships`'tedir; dolayısıyla realm'de
      kimlik doğrulayabilen herkesin person satırı olması güvenli
- [ ] Admin'in membership açabilmesi için **e-posta ile kişi arama** ucu — dar DTO, tenant-admin
      izniyle, platform-scope `GetPerson`'dan ayrı
- [ ] `POST /persons` ve `GET /persons/{id}` devre dışı kalsın; platform-admin yolu ayrı iş
- [ ] Admin panelinde "personel davet et → rol ata" akışı

## 3. Kasa oturumu + kasiyer kimlik doğrulama (tek ADR)

Tasarım referansı ve tam gerekçe: [lessons-from-odoo.md § Tier 1 madde 1 ve 4](lessons-from-odoo.md).
Bu ikisi **ayrılamaz**: oturumun fark açıklaması, arkasındaki kasiyer sunucuda doğrulanmıyorsa
hiçbir şey ifade etmez.

- [ ] **ADR:** oturum sahipliği (terminal | kasiyer | şube) + PIN doğrulama modeli
- [ ] `cash_sessions` + 4 durumlu makine (`opening_control → opened → closing_control → closed`);
      girdiler saklanır (açılış sayımı, kapanış sayımı, nakit hareketi), beklenen ve fark türetilir
- [ ] Vardiya içi nakit giriş/çıkış kaydı
- [ ] Kupür dökümlü sayım ekranı
- [ ] Kuruş yuvarlama — yalnız nakitte, fark kayıt altında
- [ ] Kapatılamama guard'ı tek fonksiyonda
- [ ] Eski oturum **uyarı** job'ı (otomatik kapatma değil — sayımı imkânsız kılar)
- [ ] Kurtarma oturumu (`rescue`) — stranded submission runbook'uyla birleştir
- [ ] PIN yalnız sunucuda doğrulanır, istemciye hash olarak bile inmez; argon2id + deneme hız
      sınırı + kilitlenme denetim izi. İstemcideki rol yalnız görünüm ipucu
- [ ] Mevcut `shifts:*` izin sözlüğünü kullan — yeni izin adı uydurma (madde 7)

Tahmin: 7–10 gün (kupür ekranı ve yuvarlama ilk tahminde yoktu).

## 4. Gün sonu satış özeti

Spec: Odoo `report_sale_details.py`. Admin dashboard bugün `mockSalesData` ile çalışıyor,
iki kart "Yakında" yazıyor.

- [ ] `GET /api/v1/pos/reports/sale-details` — tarih aralığı + şube + oturum filtresi
- [ ] Satış ve iade **ayrı akümülatörlerde**; birleştirmek iade oranını görünmez yapar
- [ ] Vergi kırılımı taban + oran ayrı (`TaxRateBPS` bunu üretmeye yeterli)
- [ ] Ödeme yöntemi başına toplam; nakitte kasa oturumu bakiyesiyle birleşik
- [ ] Admin dashboard bu uçtan beslensin

## 5. Prod deploy hedefi

`deploy/` altında yalnız `docker-compose.dev.yml` var. K8s ADR'de Faz 2'ye ertelenmiş.

- [ ] Prod compose veya K8s kararı
- [ ] Vault prod yapılandırması (bootstrap ≠ runtime)
- [ ] Yedekleme/DR'ın fiilen kurulması (ADR-OPS-001 yazılı, uygulanmamış)
- [ ] Ölçüm/alarm: reconciler overdue, outbox birikmesi

## 6. `tenant` modülü test açığı

2963 satır kaynak / 323 satır test — repodaki en düşük oran, üstelik kiracı izolasyonunu tutan
modülde. Karşılaştırma: payment 6082/7570, pos 3914/4077.

- [ ] RLS sızıntı ve cross-tenant yazma testleri, `lessons-from-b2b` invariant listesine göre

## 7. İzin wiring testi — seed'li ama bağlanmamış izinler

`000006_seed_system_roles.up.sql` şunları veriyor ama kodda karşılığı **yok**:
- `shift_manager` → `checks:approve`, `orders:approve` — `approve` yalnız `inventory`'de bağlı,
  `pos`'ta 0 çağrı yeri
- `shifts` read/create/update — `backend/internal/` altında hiç geçmiyor

`docs/lessons-from-b2b.md`'nin tam olarak önlemek için yazıldığı hata: *"Casbin RBAC init
ediliyordu → middleware 0 route'a bağlıydı."*

- [ ] **CI testi:** `role_permissions`'daki her `(resource, action)` için kodda en az bir
      `permit(...)` çağrı yeri olduğunu doğrula; olmayanda **kır**. Not değil, test
- [ ] Yönetici onayı akışı (`checks:approve`) — ikram/iskonto/iptal, sunucuda zorlanan

Bu madde 3'ten **önce** yapılmalı: kasa oturumu `shifts:*` izinlerini kullanacak, test önce
konursa yanlış izin adı uydurulması engellenir.

## 8. Token gerçek cihaz / sertifikasyon testi

Dış bağımlılık — takvimi bizde değil, şimdiden temas kurulmalı.
Açık teknik sorular `backlog-fiscal.md`'de (tokenx 401 re-auth akışı, `operationDate` timezone).

---

## Pilot kapsamı dışında — segment kararına bağlı

**Marş sistemi** ([lessons-from-odoo.md § Tier 2 madde 7](lessons-from-odoo.md)). Üst segment,
oturarak servis veren restoranda **bloklayıcı**; fast-food, kafe, paket servis, food truck'ta hiç
gerekmez. Şube düzeyinde flag'lenebilir olduğu için pilot müşteri seçildikten sonra karara bağlanır.

İçindeki para riski önemli: **tetiklenmemiş marş** adisyon toplamına, ÖKC sepetine ve stok
düşümüne girmemeli — `TestPOSSpine_ClosePaysOnlyForActiveOrders`'ın koruduğu hatanın kardeşi.
