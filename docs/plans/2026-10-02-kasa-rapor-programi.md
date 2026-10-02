# Kasa & Rapor Programı — Dondurulmuş Kararlar (2026-10-02)

Kapsam: zayi/ikram, ödemesiz kapanış, kâr/maliyet/KDV raporları, royalty, web kasa,
basit akışta KDS gizleme, zincir görünümü. Kararlar iki bağımsız mimari incelemenin
(A ve B raporları) hakem sonucudur; fazlar bu dosyayı sözleşme kabul eder.

## 0. Bloklayıcı düzeltmeler (programdan bağımsız, İLK iş)

1. **Admin `useCloseCheck` Idempotency-Key göndermiyor** (`web/apps/admin/src/hooks/use-pos.ts`)
   → `/pos/checks/{id}/close` idempotency middleware'i başlığı zorunlu tutar, düğme hep 422 alır.
   Düzeltme: mutation başına `crypto.randomUUID()` ile başlık; POS desktop'taki desenle aynı.
2. **`RegisterSale` fazla ödeme koruması yok** (`backend/internal/modules/payment/service/payment_service.go`)
   → iki kasa (POS + web) aynı adisyona eşzamanlı tahsilat yaparsa müşteriden iki kez çekilir.
   Düzeltme: ödeme yazılırken adisyon başına kilit (`pg_advisory_xact_lock(hashtext('check:'||check_id))`),
   alınmış ödemeler (pending fiscal dahil) yeniden toplanır, kalan borcu aşan tutar
   `409 payment_exceeds_due` ile reddedilir. Modül sınırı: check toplamı pos/public üzerinden.

## A. Zayi / ikram — `order_item_adjustments` (append-only)

İki rapor bağımsız olarak aynı modele vardı; `order_items`'a kolon EKLENMEZ
(app_runtime'da yalnız SELECT/INSERT var, snapshot ilkesi bozulmaz).

- Yeni tablo `pos` modülünde: `order_item_adjustments(id, tenant_id, order_item_id FK,
  kind CHECK ('comp','waste'), quantity>0 (kısmi: 3 çaydan 1'i), reason_code CHECK
  ('customer_dislike','wrong_item_prepared','quality_issue','long_wait','manager_courtesy',
  'regular_customer','staff_meal','other'), responsible_person_id UUID NULL (bare, FK yok),
  responsible_role NULL ('kitchen'|'waiter'|'bar'|'cashier'), note (other'da zorunlu — service),
  created_by, approved_by NULL, created_at)`. FORCE RLS; GRANT yalnız SELECT, INSERT.
- **`check_id` saklanmaz** (merge/move'da bayatlar; kalem id'leri move'dan sağ çıkar,
  zincir kalem→order→check ile türetilir).
- `kind='waste'` için responsible_person_id VEYA responsible_role zorunlu; comp'ta opsiyonel.
- Geri alma MVP'de yok; ileride ters kayıt (`comp_reversal`), silme asla.
- Uç: `POST /pos/checks/{id}/adjustments`, yalnız `open` adisyonda. Kilit sırası mevcutla
  aynı (önce check, sonra kalemler). Yetki: `pos.check.adjust` — shift_manager + manager
  (kasiyer PIN-switch ile).
- **Değişmez:** düzeltme sonrası yeni toplam < (ödenen + bekleyen) olacaksa
  `409 adjustment_below_paid`.
- **Tek SQL kaynağı:** `billable_lines` view/CTE — `net_qty = quantity − Σ adj.quantity`.
  `TotalsByCheckIDs` + ReportRepo'daki 4 sorgu + sale-details hepsi bunu kullanır
  (InactiveOrderStatuses "tek kaynak" kuralıyla aynı).
- **pos/public'e `BillableLinesForCheck(checkID)`** eklenir. Payment, fiscal basket'ı
  buradan türetir; istemci satır gönderirse sunucu toplamı `amount_total` ile karşılaştırır
  (bugünkü açık: istemci basket'ına körü körüne güveniliyor). POS `lib/paymentLines.ts`
  kopyası web kasada ÇOĞALTILMAZ.
- İkram edilen kalem fiscal basket'a girmez / adedi düşer (varsayılan; mali müşavir teyidi açık).
- **`comp` ve `no_charge` ödeme yöntemleri emekli:** yeni yazımda `422 method_deprecated`,
  arayüzden kalkar; geçmiş kayıtlar raporda okunur, DB constraint'i ve Token eşlemesi kalır.
  (İki paralel ikram yolu çift sayım üretir.)
- Tam adisyon ikramı = her kaleme comp kaydı; toplam 0 olur, normal Close ile kapanır.
- sale-details yeni alanlar: comp_amount/count, waste_amount/count, by_adjustment_reason[].

## B. Ödemesiz kapanış — yeni terminal durum `written_off` (hakem kararı)

Çelişki: A raporu yeni durum, B raporu `closed + close_kind='unpaid'` önerdi.
**Karar: `written_off`.** Gerekçe (fail-safe): yeni durum unutulursa rapor EKSİK gösterir
(görünür hata); `close_kind` filtresi unutulursa ciro/KDV/royalty ŞİŞER (sessiz mali hata).
Close'un "ödenen ≥ toplam" değişmezi de bozulmaz.

- Uç: `POST /pos/checks/{id}/write-off`, Idempotency-Key zorunlu.
  Gövde `{reason_code: walkout|dispute|other, note}` — note zorunlu.
- Kolonlar: `checks.close_reason_code, close_note, approved_by`; durum `written_off`
  (CHECK constraint, `allowedCheckTransitions` open→written_off, UpdateStatus closed_at CASE,
  statusTotals WHERE/switch, POS + admin durum etiketleri birlikte güncellenir).
- Kalemlere yayılmaz (kısmi ödemede hangi kalem ödendi belirsiz; zayi istatistiği kirlenmez).
- Yetki: `pos.check.write_off` — shift_manager + manager; kasiyer DATA-008 PIN-switch ile,
  switch edilen principal `approved_by`. rego + role_permissions seed + OPA matris codegen birlikte.
- Canlı siparişler: simple'da delivered (servis edildi, maliyet düşer); full'de Close kaskadıyla aynı.
  Masa `cleaning`'e geçer. Outbox: `check.written_off {total, paid, uncollected, reason_code, approved_by}`.
- Fiscal: satış yok, fiş yok (kısmi ödemenin fişi zaten basıldı). Gün sonu mutabakatı:
  Σ ödemeler = closed brüt + written_off'ların tahsil edilen kısmı — raporda açık formül.
- Gün sonu özetinde ayrı bölüm: written_off_count/total/collected/uncollected (kullanıcı isteği).

## C. Kâr / maliyet / KDV

- MVP maliyet kaynağı: `catalog.products.cost_amount BIGINT NULL` (KDV hariç kuruş) +
  `branch_product_overrides.cost_amount NULL` (DATA-009 tablosu; franchise transfer fiyatı).
- **`pos.order_items.unit_cost_amount BIGINT NULL` Place/PlaceGuest INSERT'inde snapshot** —
  kaçırılan günlerin maliyeti geri gelmez, bu yüzden şema+snapshot programın İLK faz işi.
  Öncelik: şube override → ürün maliyeti (stok/reçete türetme sonraki faz, DATA-005 taslak).
- Kâr = Σ net satır KDV hariç − Σ(net_qty × unit_cost) − ikram/zayi maliyeti (ayrı satır).
- NULL maliyet asla 0 sayılmaz; rapor "maliyeti bilinen ciro %X" kapsama oranı gösterir,
  %100 değilse kâr "kısmi" işaretli.
- KDV raporu **"Hesaplanan KDV"** etiketiyle (satış KDV'si). "Ödenecek KDV" alış faturası
  indirilecek KDV'si gerektirir → Faz 2.
- Zincir görünümü: sale-details'e opsiyonel `branch_id` — boşsa yalnız şube-kapsamsız
  manager (BranchScopeFilter) ve sonuç `by_branch[]` ile; ayrı uç açılmaz.

## D. Royalty

- `tenant` modülünde `branch_royalty_terms(tenant_id, branch_id, rate_bps 0–10000,
  basis CHECK ('gross_incl_vat','net_excl_vat','gross_profit'), include_written_off BOOL
  DEFAULT false, effective_from DATE, created_by)`; `UNIQUE(branch_id, effective_from)`,
  yalnız ekleme — geçmiş dönem bugünkü oranla yeniden hesaplanmaz.
- Yalnız `ownership_type='franchise'` şubeye kayıt (service kontrolü); tenant geneli
  varsayılan yok, arayüzde "tüm franchise şubelere uygula" kısayolu.
- Matrah closed adisyonların net faturalanabilir satırları: gross_incl_vat = Σ net_qty×unit_price;
  net_excl_vat = NewTaxLine Base toplamı; gross_profit = C'deki brüt kâr.
  İkram/zayi matraha girmez; written_off varsayılan girmez (`include_written_off` sözleşmeye göre).
- Dönem içi oran değişimi güne göre bölünür. Kâr bazlıda kapsama <%100 ise
  `royalty_incomplete=true` + uyarı.
- Hesap pos ReportService'te, sözleşme koşulu tenant/public'ten okunur.

## E. Web kasa (admin'e)

- Yeni oturum modeli YOK: web kasa şubenin açık kasa oturumuna **katılır**
  (DATA-008 Karar 1 — şube başına tek oturum; MVP kuralı: şube başına tek çekmece).
- Uçlar hazır (POS ile aynı): POST /payments, settlement, cash-sessions grubu, close.
- Asgari ekranlar: (1) adisyon detayında "Ödeme al" paneli (nakit/kart, kalan tutar,
  Idempotency-Key), (2) kasa oturumu sayfası (aç/hareket/sayım/kapat), (3) PIN ile kasiyer
  değişimi, (4) fiscal-pending göstergesi. + write-off ve adjustment aksiyonları.
- ÖKC'siz elle kart: ödemeye `fiscal_route: manual` işareti; yalnız fiscal tipi mock/none
  şubede (Token'lı şubede teknik+yasal olarak imkânsız — kart ödemesi şubenin fiscal
  tipine göre kapılanır). Yasal teyit mali müşavirde.
- Ön koşullar: Bölüm 0 (idempotency + fazla ödeme) ve A'daki BillableLinesForCheck.
- Ödeme anında settlement'taki kalan tutar yeniden doğrulanır (TOCTOU penceresi iki
  cihazla büyür).

## F. Basit akışta KDS gizleme

- `admin-sidebar.tsx`'te `usePosBranchSettings(selectedBranch).order_flow==='simple'` ise
  `/pos/kitchen` menüden çıkar. Yükleniyor/hata/"tüm şubeler" → göster (fail-open).
  sidebar-menu-config'e ayar sızdırılmaz. Doğrudan URL çalışmaya devam eder.
- Bilinen boşluk: kitchen rolünde branch-settings 403 → menü gizlenmez; kabul edildi.

## G. Kasa ödeme ekranı UX + beşli yuvarlama (2026-10-02 eki)

Kullanıcı şikâyeti: sağda fiş varken solda yeniden kalem seçiliyor; x2 kalemin 1'i
ödenemiyor; nakit küsurat yemez, beşe aşağı yuvarlama yok. Tasarım incelemesi kök
nedeni de buldu: `buildPaymentLines` tutar seçimden saparsa orantılayıp HER kalemi
"1 × paylaştırılmış fiyat" olarak ÖKC'ye gönderiyor (ad/adet yanıltıcı).

### G.1 Ödeme ekranı (karar)
- Seçim yüzeyi sağdaki adisyon (Receipt) olur; soldaki ItemPicker kalkar.
  Receipt'teki mevcut `moveSelection` seçim-kipi deseni `paySelection` olarak
  yeniden kullanılır; orta panelde Ödenecek/Alınan/numpad/kipler kalır.
- Birim bazlı seçim: `Set<itemId>` → `Map<itemId, qty>`. quantity>1 satırda dokunuş
  +1 birim, satır içi stepper [−] 1/2 [+] (≥56px), uzun basış = tümü. Kısmen ödenmiş
  satır "2× Burger — 1 ödendi, 1 kalan", yalnız kalan seçilebilir. Seçim toplamı =
  tutar olduğundan orantılama dalına düşülmez → fiş gerçek ad/adet/birim fiyatla basılır.
- Kişiler (seat) çipi "o kişinin ödenmemiş birimlerini seç" davranışına geçer
  (çoklu çip seçimi; seat backend'e yine gönderilmez).
- **MVP istemci hesaplayıcı kalır (backend değişmez)** — para güvenliği
  `payment_exceeds_due` guard'ında. **Kalıcı kalem tahsisi** (`payment_item_allocations`
  append-only: payment_id, order_item_id bare UUID, quantity_milli, amount; failed/voided
  ödeme tahsisi serbest bırakır) **Faz 2'ye eklendi** — BillableLinesForCheck ile aynı
  paket; sunucunun basket'ı kendisinin türetmesini ve `adj_qty ≤ net_qty − allocated_qty`
  kalem-düzeyi ikram kuralını mümkün kılar; tahsisli kalem taşınamaz. Web kasa (Faz 5)
  ön koşulu. Amount-bazlı ödemeler (Tümü/Böl/Başka tutar) tahsissiz kalır.

### G.2 Yuvarlama (karar)
- Politika: şube ayarı izin+sınır verir, kasiyer anlık düğmeyle uygular ("↓ 435,00'e
  yuvarla (−2,50)", geri alınabilir). Otomatik uygulama MVP'de yok.
- `pos_branch_settings`: rounding_cash_enabled, rounding_card_enabled (DEFAULT false),
  rounding_step_minor CHECK IN (50,100,500,1000) DEFAULT 500,
  rounding_max_per_check_minor DEFAULT 1000.
- Model: `payments.rounding_amount BIGINT DEFAULT 0 CHECK (>=0)`. amount_total = fiilen
  alınan; taksit adisyondan amount_total+rounding düşer. **Tek kablolama yeri:**
  `TotalPaidForCheck`/`PendingTotalForCheck` → `SUM(amount_total + rounding_amount)`
  (Close, overpayment guard, adjustment_below_paid otomatik tutarlı).
  `SumCompletedCashPayments` DEĞİŞMEZ → kasa beklenen nakdi ve sayım formülü aynı kalır.
- Sunucu kuralları (ihlal → `422 rounding_not_allowed`): rounding < step;
  amount+rounding = kalan borç (ya da kalan seçili tutar); amount % step == 0;
  şubede yöntem açık; adisyon başına Σ rounding ≤ max. Yalnız aşağı yuvarlama.
- Fiş: `FiscalSale.Discount {Description:"Yuvarlama", Value}` — Token addBasket sepet
  `adjust` sözleşmesi doğrulandı; `buildFiscalSale` dolduracak + mapper/mock'a
  `TotalMinor == Σsatır − indirim` doğrulaması eklenecek (bugün hiç yok — mevcut açık).
  Token cihazının indirimi KDV oranlarına nasıl dağıttığı DOĞRULANMADI → Token teyidine
  kadar yalnız mock/ÖKC'siz şubede açılır.
- Raporlar: ciro = yuvarlama düşülmüş net (Z raporu ile tutarlılık); brüt + "Yuvarlama
  indirimi" ayrı satır; Hesaplanan KDV orantılı dağıtımla düşer; royalty matrahı net
  üzerinden (sözleşme notu). B mutabakat formülü genişler:
  Σ(amount_total+rounding) = closed brüt + written_off tahsil edilen.

## Uygulama sırası

1. **Faz 0:** Bölüm 0'ın iki bloklayıcısı (+ regresyon).
2. **Faz 1:** C şeması + snapshot (veri geri kazanılamaz) + F (yarım gün).
3. **Faz 2:** A (tablo, billable_lines, BillableLinesForCheck + basket doğrulama,
   comp/no_charge emekliliği) + B (write-off).
4. **Faz 3:** sale-details genişletmesi, gün sonu özet bölümleri, kâr raporu,
   "Hesaplanan KDV", zincir görünümü (by_branch).
5. **Faz 4:** D (royalty).
6. **Faz 5:** E (web kasa UI).

Her faz: implementasyon alt agent'ta, tam regresyon (backend `-p 1`, vitest, lint,
arch-lint) + deploy + canlı doğrulama + etkilenen e2e-prod spec'leri.

## Mali müşavir / ürün teyidi bekleyenler (varsayılanla ilerleniyor)

| Soru | Varsayılan |
|---|---|
| İkram fişte görünecek mi? | Hayır — basket'tan düşer, raporda ayrı satır |
| Kaçan müşteriye fiş kesilecek mi? | Hayır — satış yok, mali kayıt yok |
| ÖKC'siz elle kart yasal mı? | Yalnız mock/ÖKC'siz şubede, `fiscal_route: manual` işaretli |
| `comp` ödeme yöntemi emekli mi? | Evet — yeni yazım 422, geçmiş okunur |
