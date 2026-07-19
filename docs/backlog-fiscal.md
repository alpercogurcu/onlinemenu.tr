# Fiscal / Ödeme Takip Listesi

2026-07-19 fiscal takip sprinti sonunda açık kalan işler. Repo'da Jira olmadığı için
kalıcı kayıt burada tutulur; bir madde tamamlanınca bu dosyadan silinir.

## Yüksek öncelik

- **Kasiyer için check-scoped "ödenen" okuması.** Çözüldü — `GET /api/v1/payments/checks/{id}/settlement`
  (dar DTO, `payment.fiscal_status.read`, şube filtreli) + POS istemcisinde kasiyer oturumunun
  `serverCompleted` haritasının bu uçtan dolması. 5 dk pencere sınırı tamamen kalktı.
  **Açık iş:** CI'da `pnpm typecheck` koşmuyor (elle yazılan wailsjs binding sapmaları görünmez —
  `wails generate module` adımı da düşünülmeli).
- **Stranded pending kurtarma.** Backend tarafı çözüldü — bkz.
  [runbook](runbook-fiscal-stranded.md). Manager-only manuel expire ucu eklendi
  (`POST /api/v1/payments/fiscal/submissions/{id}/expire`, `payment.fiscal_terminal.manage`);
  sonuç reconciler'ın expire zinciriyle aynı (`OnFiscalResult` → ödeme `failed`), denetim izi
  `result_payload` + log. **Açık iş:** kasiyer arayüzünde eskalasyon yolu (POS'ta "yöneticiye
  bildir" akışı) ve reconciler overdue uyarısına alarm bağlanması.
- **Şube-bazlı roller için membership kısıtı.** Çözüldü — bkz.
  [ADR-SEC-005](adr/SEC-005-branch-scoped-membership.md) ve identity migration
  `000012_memberships_branch_scoped_guard`. `roles.branch_scoped` bayrağı + memberships
  trigger'ı zincir-geneli yetki sızıntısını DB seviyesinde kapatıyor. **Açık iş:** ADR'deki
  deploy-öncesi (duplicate membership, klon fingerprint) ve deploy-sonrası (ihlal denetimi)
  sorguları prod'da çalıştırılmalı; `warehouse` rolünün ADR-DATA-005 ile çelişkisi kararı bekliyor.

## Orta öncelik

- **`memberships.tenant_id` ↔ `roles.tenant_id` eşitliğini zorlayan kısıt yok.** SEC-005 trigger'ının
  RLS fail-closed reddi bunu kısmen kapatıyor (görünmeyen rol → 23514) ama asıl bütünlük kuralı DB'de
  ifade edilmiş değil; ayrı ele alınmalı.
- **Custom rol API'sinde `branch_scoped` taşınmıyor.** Tenant'ın oluşturduğu custom roller daima
  `branch_scoped=FALSE` doğuyor; rol oluşturma/güncelleme yüzeyine alan eklenmeli (bkz. SEC-005 "açık işler").

- **`FiscalResult.CompletedAt` invaryantını sözleşmeye bağla.** "CompletedAt = sunucu saati"
  şu an dokümantasyonla korunuyor; `OnFiscalResult` girişinde damgalamak (veya adapter
  sözleşmesine yazmak) ileride senkron bir sürücünün cihaz saati sızdırmasını önler.
  `MarkResult`'taki `COALESCE($5, NOW())` ölü dalı da aynı işte kapatılmalı.
- **Token `operationDate` timezone netleştirmesi.** tz-naive layout'lar UTC varsayılıyor;
  cihaz İstanbul yerel saati gönderirse `fiscal_receipts.issued_at` ~3 saat kayar (yalnız
  yasal damga; poll penceresi artık etkilenmiyor). Token ticari temasında sorulacak;
  netleşene kadar naive layout'lar için `Europe/Istanbul` varsayımı değerlendirilebilir.
- **arch-lint gerçek ihlalleri.** `go-arch-lint check` artık koşuyor ama bildirimler var:
  `payment_http → payment/repo, payment/fiscal/tokenx` doğrudan bağımlılıkları,
  `payment/fiscal/tokenx`'in hiçbir component'e bağlı olmaması, ~30 `_test` self-reference
  eksiği. CI gate'i yeşile çekmek için config + yapısal düzeltme kararı gerekiyor.
- **Worker cycle-arası kesin fiş sıralaması.** `MarkRetry` + çoklu process senaryosunda
  sıralama garantisi process-içi; kesin çözüm claim sorgusunda per-terminal lease.

## Düşük öncelik

- Webhook endpoint'i gözlemlenebilirlik kör noktası (otelhttp WithFilter ile span+metrik
  tamamen kapalı) — ADR notu + filter'ın payment modülünden `fx.Provide` ile sağlanması.
- POS `Receipt.tsx` uzak pending/settled ödemeleri satır olarak göstermiyor (yalnız bakiye
  düşürüyor); `receivedTotalForPrint` uzak tahsilatı "ALINAN"a katmıyor.
- 409 dışındaki hata gövdeleri (404/403/422/500) hâlâ düz metin; tümünü `{error, code}`
  JSON'a taşıma kararı.
- `payments` tablosuna `failure_reason` alanı (şimdilik `fiscal_submissions.last_error`).
- `fiscal_submissions` settled indeksi sınırsız büyüyor — retention/partition planı (ROADMAP).
- pos/ws hub'ı her order event'inde DB'den order okuyor (payload `branch_id` taşımıyor);
  outbox payload zenginleştirmesi.
- FiscalStatusBadge palet çelişkisi (`--color-danger` yalnız void için ayrılmışken failed
  kırmızı) — ui-designer hakemliği.
- pos-desktop `typecheck` CI'da koşmuyor (wailsjs üretilmiş dizin gerektiriyor).
- tokenx 401 akışı: aynı çağrı içinde retry yok, sonraki çağrıda re-auth — Token gerçek
  cihaz/sertifikasyon testinde teyit edilmeli.
- POS poller backoff (3sn→15sn) zamanlaması test edilmiyor (interval enjeksiyonu refactor'ü).
