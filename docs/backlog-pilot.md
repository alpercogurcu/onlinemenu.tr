# Pilot Takip Listesi — İlk Satışa Kadar

> Oluşturma: 2026-07-31 · Son büyük güncelleme: 2026-09-06 · Kapsam: **online-only pilot** (tek şube, tek kasa, sabit internet, edge-sync yok)
> Repo'da Jira yok; kalıcı kayıt burada tutulur. Bir madde tamamlanınca bu dosyadan silinir.
> Kardeş listeler: [backlog-fiscal.md](backlog-fiscal.md) · Referans: [lessons-from-odoo.md](lessons-from-odoo.md)
> 2026-09-05/06 sprint planı ve kararları: [superpowers/plans/2026-09-05-pilot-mvp.md](superpowers/plans/2026-09-05-pilot-mvp.md)

**Ürün kararı (2026-07-31):** offline-first ilk satışa girmiyor. Pilot müşteri sabit internetli,
tek şubeli bir işletme olacak. `edge-sync` Faz 2'ye kalır; ROADMAP Faz 1'in "satılabilir MVP"
tanımı bu pilottan sonra tamamlanır.

### Pilot müşteri seçim kriterleri (bağlayıcı)

| Kriter | Neden |
|---|---|
| Sabit, güvenilir internet | `edge-sync` yok; bağlantı koparsa satış durur |
| Tek şube | Zincir-geneli senaryolar test edilmedi |
| **Tek kasa (tek para çekmecesi)** | ADR-DATA-008: kasa oturumu şube başına. İki çekmece tek sayıma inerse mutabakat anlamsızlaşır |
| Marş kullanmayan segment tercih edilir | Üst segment oturarak servis marşı gerektirir; fast-food/kafe/paket servis gerektirmez |

---

## Halihazırda çalışan (2026-09-06 itibarıyla)

- Satış omurgası uçtan uca test edilmiş (`internal/e2e/spine_test.go`): adisyon aç → sipariş → ödeme → ÖKC mali kayıt → settle → kapat. Token/Beko X30TR entegrasyonu gerçek.
- Personel onboarding: admin panelden davet (ADR-AUTH-003), Keycloak Admin API, `settings/users` ekranı.
- Kasa oturumu + kasiyer PIN (ADR-DATA-008): açılış/sayım/kapanış, nakit hareket kaydı **ve defteri** (`GET /cash-sessions/{id}/movements`), kasa açılmadan nakit satış reddi, eski oturum uyarı izleyicisi (15 dk / 20 sa, yalnız Warn log).
- QR dine-in online sipariş (ARCH-006): public menü, misafir oturumu, müşteri uygulaması (`web/apps/menu`), admin QR yönetimi.
- KDS: canlı WebSocket kanban, snapshot N+1 giderildi, istemci tarafı toplu sipariş detayı (`GET /pos/orders?ids=`).
- **Gün sonu satış özeti**: `GET /api/v1/pos/reports/sale-details` (satış/iptal ayrı, KDV kırılımı, gün ve kaynak kırılımı, ödeme yöntemi × durum, kasa oturumları). Admin dashboard bu uçtan besleniyor; `pos.report.read` yalnız shift_manager + manager.
- Prod: `deploy/docker-compose.prod.yml` (tam stack), Postgres bootstrap ve compose-içi yedekleme (ADR-OPS-001), Keycloak SMTP şablonu (`${SMTP_*}` realm import), `/healthz` + `/readyz`, outbox/fiscal-overdue gauge'ları + Prometheus alarm kuralları, `deploy/smoke.sh` (`task deploy:smoke`).
- CI: lint (`GOTOOLCHAIN=go1.25.5` pinli), race'li testler, gosec/trivy, migration dry-run, admin/menu/pos-desktop test ve typecheck.

---

## Açık maddeler

### 1. Prod kurulumu — gerçek sunucuya ilk deploy

Compose ve şablonlar hazır; hiç gerçek ortama kurulmadı.

- [ ] Sunucu + ters proxy (TLS) kurulumu; `/readyz` ve `/healthz` dışarıya açılıyorsa rate-limit'e alınması (bkz. Task 7 review notu)
- [ ] `.env.prod.sops` üretimi (SOPS/age), SMTP bilgileri dahil — şablon: `deploy/.env.prod.example`
- [ ] `task deploy:smoke` ile canlı doğrulama
- [ ] Vault: bugün yalnız Keycloak admin-client secret'ı Vault'tan okunuyor; DB/NATS/TokenX sırları `.env.sops` üzerinden düz env. Pilot için kabul; Faz 2'de dynamic secrets (CLAUDE.md düzeltildi)
- [ ] Alertmanager / bildirim kanalı **yok** — `deploy/prometheus/rules.yml` kuralları tetiklense de kimseye ulaşmıyor (plan R6, ürün kararı bekliyor). Ek kural önerisi: `absent(onlinemenu_outbox_pending)` (dispatcher kapalıyken seri doğmuyor)
- [ ] `deploy/prometheus/prometheus.yml` `external_labels` dev/prod paylaşımlı (`environment=development`) — prod için ayrıştırılmalı
- [ ] SEC-005 deploy-öncesi/sonrası sorguları prod'da koşulmalı (bkz. backlog-fiscal.md)

### 2. Personel onboarding — kapatılmamış açıklar

- [ ] **`persons.email` Keycloak ile senkron değil.** Realm yöneticisi e-postayı değiştirirse sonraki davet ikinci kullanıcı yaratıp `persons_email_idx` çakışmasıyla 500'e düşer
- [ ] **Şube varlığı doğrulanmıyor.** `memberships.branch_id → branches` FK'sı modül izolasyonu için kaldırıldı; davet var olmayan şubeye membership yazabilir
- [ ] SMTP prod'da fiilen zorunlu (şablonda var, gerçek değerler kurulumda girilecek)

### 3. Kasa oturumu — kalanlar

- [ ] Kuruş yuvarlama — yalnız nakitte, fark kayıt altında (plan R4: ertelendi, tutarlar tam kuruş)
- [ ] Kurtarma oturumu (`rescue`) — stranded submission runbook'uyla birleştir
- [ ] **Vardiyaya katılım otomatik değil** — ürün kararı bekliyor (plan R5)
- [ ] Eski oturum izleyicisi metrik yaymıyor (yalnız log, oturum başına bir kez); alarm istenirse `open_cash_sessions_past_max_age` gauge'ı
- [ ] POS defter görünümü: yükleme hatasında "hareket yok" metni bastırılmalı, önceki oturumun satırları temizlenmeli
- [ ] `RequireOpenSession` her istekte tam transaction açıyor — 500 POS hedefinde ölçülmeli (`task backend:loadtest:smoke`)

### 4. Gün sonu raporu — takipler

- [ ] Business-day ofseti (ADR-DATA-003 taslak; `branch_settings.business_day_offset` okunmuyor). İstemci takvim günü sınırlarını gönderiyor; 04:00 kesimi isteyen müşteri gelirse sunucuda `from/to` kaydırılır (plan R3)
- [ ] `checks` için `(tenant_id, branch_id, status, closed_at)` indeksi — veri büyüyünce
- [ ] Rapor 422/403 gövdeleri düz metin (kod alanı yok); admin istemcisi metin eşleştirmemeli
- [ ] `domain.NewTaxLine` negatif/absürt bps için guard'sız (DB verisinden erişilemez)
- [ ] Dashboard yetkisiz rolde de istek atıyor (403 alıyor) — kozmetik kapı var, istek de kapatılabilir

### 5. Test kapsamı borçları (bu sprintte kayda geçen minörler)

- [ ] Join 403 testleri gerçek chi router + `permit` üzerinden koşmuyor; `listCashMovements` handler testi yok
- [ ] `report_repo` testinde `rejected` sipariş statüsü seed'lenmiyor
- [ ] Admin lint tabanı ~470 uyarı (`react-hooks/set-state-in-effect` deseni yaygın) — ayrı temizlik
- [ ] `useOrderDetails`: kalıcı olarak başarısız bir sipariş id'si her liste değişiminde yeniden istenir — backoff
- [ ] `tenant` modülü test oranı hâlâ düşük; invariant testleri (952ab1c) var, RLS sızıntı/cross-tenant yazma matrisi `lessons-from-b2b` listesine göre tamamlanmalı

### 6. İzin sözlüğü sapmaları

- [ ] Yönetici onayı akışı (`checks:approve`) — ikram/iskonto/iptal, sunucuda zorlanan (fark onayı ertelendi, 2026-08-03 kararı)
- [ ] ⏸️ `driver` rolü: seed izin veriyor, OPA'da allow yok (şoför/teslimat kapsam dışı)
- [ ] `kitchen`/`bar` `inventory:read` seed'i ADR-DATA-005 ile çelişiyor — seed satırlarını kaldırma kararı

### 7. Token gerçek cihaz / sertifikasyon testi

Dış bağımlılık — takvimi bizde değil. Açık teknik sorular `backlog-fiscal.md`'de.

### 8. Faz 2'ye devredilenler (pilot sonrası)

- `/readyz` yalnız `cmd/api`'de; split binary'ler (`api-core/pos/finance`, `edge`) yalnız `/healthz` taşıyor
- Vault dynamic secrets (DB/NATS/TokenX)
- `edge-sync` offline mod
- Marş sistemi, şoför/teslimat, imalat (2026-08-01 kapsam kararı)

---

## Kararlar (özet)

- **2026-08-03:** Keycloak davet entegrasyonu → yapıldı. Kasa açılmadan nakit satış → engellendi (`b1c5691`). Fark onayı → ertelendi, denetim izi yeterli.
- **2026-09-05 (plan R1-R8):** rapor ucu `pos` modülünde, ödeme kırılımı `payment/public.SalesSummaryReader` üzerinden; satış = pencerede kapanan adisyonlar, iptal ayrı akümülatör; business-day ofseti uygulanmadı; kuruş yuvarlama ve otomatik vardiya katılımı ertelendi; Alertmanager kurulmadı; Keycloak SMTP realm import `${VAR}` yer tutucuyla; iş `feat/pilot-mvp` dalında, `main`'e merge kullanıcı kararı.
- **2026-09-05:** `/readyz` ham DB hatasını istemciye döndürmez (kimliksiz uç); hata yalnız log'da.

### Mutfak ekranı — mevcut durum

KDS **çalışıyor**: `admin/(main)/pos/kitchen` 4 sütunlu kanban, `pos/ws` hub'ı üzerinden canlı akış,
bağlantı durumu rozeti, accept/advance aksiyonları, toplu detay yükleme. Marş dışında bilinen işlevsel boşluğu yok.
