# ADR-FISCAL-002: Fiscal Adapter v2 — Asenkron Kayıt Modeli ve Token X Connect Entegrasyonu

**Durum:** Öneri (Taslak)
**Tarih:** 2026-07-10
**İlgili:** ADR-FISCAL-001 (interface temeli), ADR-SEC-003 (idempotency), ADR-DATA-001 (outbox)
**Kategori:** Mali (FISCAL)

## Bağlam

İlk gerçek ÖKC entegrasyonu Beko X30TR (Token X Platform) ile yapılacak. Araştırma (2026-07-10, developer.tokeninc.com + Token MCP) iki entegrasyon yolu ortaya koydu:

| | X Connect Cloud (kablosuz) | X Connect Wire (kablolu) |
|---|---|---|
| Taşıma | REST API + webhook | USB Type-C, C#/.NET DLL (Windows) veya C++ DLL/.so (Windows/Ubuntu 18.04+ 64-bit) |
| Sepet gönderme | `POST /v1/basket` (`branch-id` header) | `sendBasket(JSON)` |
| Sonuç | `BASKET_COMPLETED` webhook (status 0/-1/99, receiptNo, zNo, paymentItems) | `serialInCallback(type=3, JSON)` — aynı şema |
| Kimlik | client-id/secret → 24 saatlik JWT | ECDSA cihaz sertifikası (DLL içinde) |
| Kısıt | Polling yasak (429), webhook zorunlu; sepet TÜM şube terminallerine düşer | Tek PC'ye tek cihaz; cihaz "satış ekranında" olmalı (type=9 hatası) |
| Offline | Belirsiz (dokümante değil — Token'a soruldu) | USB yerel; bağlantı kopsa bile satış cihazda tamamlanır, yeniden bağlanınca sonuç döner |

**Kritik gözlem:** İki yol da aynı sepet JSON modelini kullanır (items, paymentItems, documentType, adjust, customerInfo). Tek fark taşıma katmanı ve sonuç kanalıdır.

**Çoklu-üretici zorunluluğu:** Beko/Token ilk adaptördür; Hugin, Profilo, Ingenico YN, Verifone YN gelecektir. Token'a özgü hiçbir kavram (ödeme tipi kodları, sectionNo, quantity×1000, cihaz-üstü hesap bölme) domain'e veya POS akışlarına sızmamalıdır.

## Sorunlar (mevcut kod)

1. `FiscalDeviceAdapter.RegisterSale` **senkron** ve `WithTenantTx` içinde çağrılıyor (`payment_service.go`). Gerçek cihazda bu, saniyeler/dakikalar sürebilecek bir işlemi (garsonun cihazda ödemeyi almasını beklemek) DB transaction'ı içinde bekletmek demek — imkânsız.
2. `FiscalSale` yalnızca toplam tutar taşıyor; kalem, KDV, kısım yok. Tüm ÖKC'ler kalem bazlı sepet ister.
3. Adapter seçimi `module.go`'da mock'a sabitlenmiş; `branch_settings.fiscal_device_type` factory'si yok.
4. `PaymentMethod` yalnız `cash|terminal`; sahada ikram, ödemesiz, yemek kartı, açık hesap gibi tipler var.

## Karar

### 1. İki fazlı (asenkron) mali kayıt modeli

`RegisterSale` yerine **submit + sonuç** ayrımı:

```go
// internal/modules/payment/domain — vendor-bağımsız
type FiscalDeviceAdapter interface {
    // SubmitSale sepeti cihaza/buluta iletir, kaydı BAŞLATIR. Bloklamaz.
    SubmitSale(ctx context.Context, sale FiscalSale) (FiscalSubmission, error)
    // VoidSale iptal fişi başlatır (isVoid=true sepet).
    VoidSale(ctx context.Context, ref FiscalSubmissionRef) error
    // Capabilities adapter'ın desteklediği özellikleri bildirir.
    Capabilities() FiscalCapabilities
}

// FiscalResultSink: adapter'lar sonucu (webhook/callback) normalize edip buraya iletir.
// PaymentService implemente eder; ödemeyi pending→completed geçirir.
type FiscalResultSink interface {
    OnFiscalResult(ctx context.Context, res FiscalResult) error
}
```

Akış:
1. POS ödeme başlatır → `payments` satırı `pending` + `fiscal_submissions` satırı aynı transaction'da yazılır (outbox deseni — ADR-DATA-001 dispatcher'ı yeniden kullanılır).
2. Dispatcher/worker `SubmitSale` çağırır (transaction DIŞINDA) → sepet Token Cloud'a gider.
3. Webhook (`BASKET_COMPLETED`) gelir → adapter normalize eder → `OnFiscalResult` → ödeme `completed`, `fiscal_receipts` yazılır (receiptNo, zNo, UUID, ham payload), `payment.completed` outbox'a düşer.
4. status -1/99 → ödeme `failed`/`voided`; POS'a real-time bildirim (mevcut WS altyapısı).
5. Mock adapter senkron çalışmaya devam eder: `SubmitSale` içinde anında `OnFiscalResult` çağırır — CI/dev akışı değişmez.

**Idempotency:** `basketID` = `fiscal_submissions.id` (UUID v4). Webhook işleme `ON CONFLICT (event_uuid) DO NOTHING` (ADR-DATA-002).

**Uzlaştırma (reconciliation):** Webhook kaçarsa `Get Open Baskets For Terminal` ile periyodik tarama (asynq job). Sepetler Token tarafında azami 2 hafta yaşar; bizim submission'lara da TTL + uyarı konur.

### 2. Vendor-bağımsız sepet modeli

```go
type FiscalSale struct {
    SubmissionID uuid.UUID       // basketID olarak kullanılır
    TenantID     uuid.UUID
    BranchID     uuid.UUID
    CheckID      *uuid.UUID
    Lines        []FiscalLine     // kalem bazlı — zorunlu
    Payments     []FiscalPayment  // ödeme planı (bizim POS'ta seçilen)
    Discount     *FiscalAdjust
    Customer     *FiscalCustomer  // e-fatura/bilgi fişi için
    Meta         FiscalMeta       // TableLabel, WaiterName, CheckNumber
}

type FiscalLine struct {
    Name           string
    UnitPriceMinor int64  // kuruş
    QuantityMilli  int64  // 1000 = 1 adet (Token ile aynı çözünürlük, vendor'a map edilir)
    TaxRatePermyriad int  // 1000 = %10 (yüzde×100)
    CategoryID     uuid.UUID // catalog kategorisi → cihaz kısmına eşlenir
    Unit           string    // UN/ECE kodu (C62, KGM, LTR...)
}
```

- **PaymentMethod genişler:** `cash | card | meal_card | comp (ikram) | no_charge | open_account | ...` — Token kodlarına (1/3/7/9/8/17) eşleme **yalnız adapter içinde**.
- **Kısım eşleme:** cihaz kısımları (`Get Fiscal Parameters` / `getFiscalInfo`) tenant/branş bazında senkronize edilip `fiscal_device_sections` tablosunda tutulur; catalog kategorisi → kısım eşlemesi admin panelden yapılır. `sectionNo`/`taxPercent` asla hardcode edilmez (Token kritik kuralı; mali tutarsızlık riski).
- KDV oranı bizim katalogdan gelir ama sepete cihazın kısım KDV'si yazılır; uyuşmazlıkta submit reddedilir ve admin uyarılır (yasal tutarlılık).

### 3. Cihaz yeteneği bildirimi (capability flags)

```go
type FiscalCapabilities struct {
    OnDeviceSplit    bool // Token X30TR: true — ama bizim POS split'i birincil akış kalır
    VoidSale         bool
    CustomerInfo     bool
    CurrencyPayment  bool
    OperatorRouting  bool // operatorId ile banka/yemek kartı uygulaması seçimi
}
```

**İlke:** POS özellikleri (hesap bölme, kısmi ödeme, ikram) **bizim POS'ta** yaşar; cihaz-üstü muadilleri yalnız capability varsa opsiyonel kısayol olarak sunulur. Bu, Hugin/Ingenico eklendiğinde özellik kaybı yaşanmamasını garanti eder.

### 4. Adapter yerleşimi ve factory

```
internal/modules/payment/fiscal/
├── mock/            # mevcut mock (senkron sink çağrısı)
├── tokenx/          # Token X Platform — Beko X30TR
│   ├── mapper.go    # FiscalSale ↔ Token sepet JSON (Wire+Cloud ortak)
│   ├── cloud/       # REST client + webhook normalize (Faz 1 hedefi)
│   └── wire/        # (ertelenmiş) DLL köprü istemcisi
└── factory.go       # branch_settings.fiscal_device_type + fiscal_device_config
```

- `fiscal_device_type` enum'una `beko_x30tr_cloud` eklenir (ADR-FISCAL-001 şeması).
- `fiscal_device_config` (JSONB): terminal listesi, varsayılan sepet modu, operatorId tercihleri.
- Token client-id/secret **Vault'ta** tutulur (platform/vault); 24 saatlik JWT bellekte cache'lenir, tek uçuş (singleflight) ile yenilenir. 429 için 1s/2s/4s, azami 3 retry (Token kod standardı).

### 5. Webhook girişi ve terminal kayıt defteri

- Endpoint: `POST /webhooks/fiscal/tokenx` (public, tenant-bağımsız giriş).
- Eşleme: `fiscal_terminals` tablosu — `terminal_id` (örn. `AV0000000658`) → tenant/branch/adapter. Kayıt, cihaz QR'ından okunan `merchantId_branchId_terminalId` ile admin panelden yapılır.
- Güvenlik: Token webhook imzası dokümante değil (açık soru). O gelene dek: URL'de tahmin edilemez tenant-secret path segmenti + TokenX IP allowlist + payload'daki `clientId` doğrulaması.
- Webhook handler yalnız kuyruğa yazar (hızlı 200); işleme asynq worker'da.

### 6. Sepet modu stratejisi (Cloud)

TokenX Connect cihaz uygulamasında iki mod vardır ve mod **yalnız cihaz üstündeki fiziksel butonla** değişir (API yok); şube tek modda standartlaşmalıdır:

| Mod | API | Davranış |
|---|---|---|
| Sepet Ödemesini Hemen Al | `Add Instant Basket` (terminal-id) | Cihaz doğrudan ödeme ekranına atlar; ödeme sonrası sepet otomatik kapanır. Kasiyer cihazda menü gezinmez. |
| Birden Fazla Sepeti Listele | `Add Basket` (branch-id) | Sepetler cihazda listelenir; ödeme için cihaz ekranında sepet seçmek gerekir. |

**Varsayılan akış: instant mod.** Adisyonlar bizim POS'ta yaşar (master); Beko'ya yalnız ödeme anında, hedef terminale instant basket gönderilir. Cihaz mali kayıt + ödeme yürütücüsüdür — çoklu-üretici ilkesiyle uyumlu, adisyon aynalama senkron yükü (her sipariş değişikliğinde PUT, LOCKED çakışmaları) yoktur. Liste modu (adisyonların cihazda tutulması) isteyen işletmeler için `fiscal_device_config.basket_mode` ile şube bazlı opsiyoneldir; bu modda ödemenin cihazdan seçilerek alındığı UX'e yansıtılır.

### 7. Wire desteği (ertelenmiş tasarım kararı)

Wire istendiğinde **cgo değil, köprü süreci**: POS bilgisayarında küçük bir Windows servis (.NET, IntegrationHub.dll'i saran) localhost gRPC/HTTP sunar; edge-sync local server veya Wails uygulaması bu köprüyle konuşur. Gerekçe:
- DLL'in birincil yüzeyi C#/.NET; C++ DLL'in header/ABI'si dokümante değil.
- Callback'ler + singleton + thread kısıtları cgo'da kırılgan; ayrı süreç çökme izolasyonu sağlar.
- `tokenx/mapper.go` aynen kullanılır — köprü yalnız taşıma katmanıdır.

## Sonuçlar

**İyi:** Payment hot path'i cihaz beklemez; çoklu-üretici hazır; mock/CI akışı korunur; Wire eklemek yalnız taşıma katmanı işi.

**Dikkat:**
- Ödeme "başlatıldı ama sonuçlanmadı" ara durumu POS UX'ine eklenmeli (bekleyen mali kayıt göstergesi, yeniden deneme/iptal).
- `branch-id` ile gönderilen sepet şubedeki TÜM terminallere düşer — terminal seçimi cihaz başındaki personelde. UX bunu varsaymalı.
- `receiptLimit` (fiscal info) aşımında bilgi fişi + fatura akışı (documentType 9005/9006/9007) billing modülüyle koordine edilmeli; 2026 KDV dahil fatura eşiği 12.000 TL.

**Riskler / Açık sorular (Token ekibine):**
1. X30TR data kartı (SIM): şube interneti kesikken cihaz Cloud sepetlerini alabiliyor mu? Bizim backend→Token yolu buluttan buluta olduğundan şube interneti bizim tarafı etkilemez; kritik olan cihazın bağlantısı.
2. Webhook teslimat retry politikası (endpoint'imiz geçici düşerse)?
3. Terminal başına açık sepet limiti (hata 1100 hangi modda tetiklenir)?
4. Webhook imza/doğrulama mekanizması var mı? TokenX IP listesi?
5. Ödeme tipi 4, 5, 6 neden tanımsız; operatorId tam listesi (`/pages/TcAMM4CprhPvPaGgovqp`)?
6. Cloud onboarding: client-id/secret edinme, test cihazı, ücretlendirme. *(Kısmen cevaplandı: "Müşteri Kayıt ve Kurulum Süreci" dokümanına göre entegratör önce **Beko ÖKC Uyumluluk Testi** onayı alır, tokeninc.com entegrasyon sayfasında sektör bazlı listelenir; müşteri entegratörü seçip cihaz başına entegrasyon satın alır — X30TR'de uygulama ataması anlık, 300TR'de servis ziyareti gerekir. client-id/secret ve ücretlendirme detayı hâlâ açık.)*
7. Tam ödeme planlı (nakit) sepette cihaz başında onay gerekiyor mu, fiş otomatik mi basılıyor?
8. Sepet modu cihaz başına ayarlandığına göre aynı şubede terminaller farklı modlarda karışık çalışabilir mi (biri instant, biri liste)? `Add Basket` branch-id ile tüm terminallere gittiğinde instant moddaki terminal bu sepetleri görmezden mi gelir?
9. Cihaz üzerinden entegrasyon sepetine ürün eklenebilir/çıkarılabilir mi? Dokümante webhook'lar yalnız `BASKET_COMPLETED/LOCKED/UNLOCKED` — içerik değişikliği event'i yok; cihazdan sipariş girişi görünüşe göre desteklenmiyor (tasarım varsayımı: cihaz salt-görüntüle + ödeme-al).
10. Aynı `basketID` ile ikinci `POST /v1/basket` ne yapar — hata mı döner, ikinci sepet mi oluşur? Doküman tanımsız bırakıyor; `PUT /baskets/{id}`'nin ayrıca var olması POST'un idempotent OLMADIĞINI ima ediyor. Sertifikasyon ortamında test edilip sonucu buraya işlenecek. O güne dek worker'ın stale-reclaim penceresi (10 dk) adapter'ın en kötü gönderim süresinin çok üzerinde tutulur; çok-replikalı dağıtımda ek koruma (reclaim öncesi `GET /baskets/{id}` varlık kontrolü) gerekir.

## Değerlendirilen Alternatifler

- **Senkron RegisterSale'i koruyup timeout büyütmek:** Reddedildi — garsonun cihazda ödeme almasını DB transaction'ında beklemek ölçeklenemez ve kilitlenme üretir.
- **cgo ile C++ DLL/.so bağlama:** Ertelendi — ABI dokümante değil, callback/thread modeli belirsiz, çökme izolasyonu yok. Köprü süreci tercih edildi.
- **Cihaz-üstü split'i birincil akış yapmak (Token önerisi):** Reddedildi — vendor kilidi; Hugin/Ingenico bu akışı desteklemeyebilir. Capability olarak opsiyonel sunulacak.
- **Tek "Token adapter" (Cloud+Wire ayrımı yok):** Kısmen kabul — mapper ortak, taşıma katmanları ayrı paket.
