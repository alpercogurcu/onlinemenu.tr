# Fiscal / Ödeme Takip Listesi

2026-07-19 fiscal takip sprinti sonunda açık kalan işler. Repo'da Jira olmadığı için
kalıcı kayıt burada tutulur; bir madde tamamlanınca bu dosyadan silinir.

## Yüksek öncelik

- **Kasiyer için check-scoped "ödenen" okuması.** `recently_settled` 5 dakikalık pencere
  sonrasında kasiyer oturumunda adisyon bakiyesi tam tutara geri sıçrar → çifte tahsilat
  penceresi (POS istemcisi 5 dk boyunca koruyor, sonrası açık). Kalıcı çözüm:
  `payment.payment.read`'i genişletmeden, kasiyere açık dar bir check-scoped ödenen-toplam
  okuması. (Bkz. `web/apps/pos-desktop/frontend/src/lib/fiscalStatus.ts` "KNOWN LIMIT" yorumu.)
- **Stranded pending kurtarma runbook'u/UX'i.** `AutoExpire` bilinçli kapalı (ADR-FISCAL-002)
  ve `VoidSale` pending submission'ı reddediyor → webhook hiç gelmezse adisyon kalıcı
  `fiscal_pending`'de kilitlenir; tek çıkış operatör müdahalesi. Gerçek cihaz testinden önce
  kurtarma prosedürü (ve kasiyer arayüzünde eskalasyon yolu) kararlaştırılmalı.
- **Şube-bazlı roller için membership kısıtı.** `memberships.branch_id` NULL'lanabiliyor ve
  cashier gibi şube-bazlı sistem rollerini non-null şubeye bağlayan kısıt yok; payment
  `requireBranch` sertleştirildi ama kalıcı çözüm kararı açık: DB CHECK (şube-bazlı roller ⇒
  `branch_id NOT NULL`) veya paylaşılan `auth.Principal.HasBranchAccess` daraltması.
  Deploy öncesi veri kontrolü: manager olmayan NULL-branch membership var mı?

## Orta öncelik

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
