# ADR-FISCAL-001: Fiscal Device Adapter Interface

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-7 (v2'de P1'den P0'a yükseltildi)
**Kategori:** Mali (FISCAL)

## Bağlam

Türkiye'de restoran, bar, market, food truck için perakende satışlar **YN ÖKC (Yeni Nesil Ödeme Kaydedici Cihaz)** üzerinden kayıt edilmek zorunda. GİB (Gelir İdaresi Başkanlığı) bu zorunluluğu tüm perakende işletmelere yaydı. ÖKC'den geçmemiş satış yasal değildir; işletme ceza + ruhsat askısı riski altındadır.

ÖKC cihazlarının markaları farklı (Hugin, Profilo, Beko, Ingenico YN, Verifone YN) ve her birinin SDK'sı/protokolü bağımsız. Payment modülü fiscal entegrasyon düşünülmeden yazılırsa Faz 2'de her marka için modülü yarı yarıya yeniden yazmak gerekir.

**v2 kararı:** Gerçek cihaz SDK entegrasyonları Faz 2'de kalır; ancak **interface + event şemaları + mock adapter + şema değişiklikleri Faz 0'da yapılır.**

## Karar

### 1. Fiscal Device Adapter Interface

`internal/modules/payment/public/fiscal.go`:

```go
type FiscalDeviceAdapter interface {
    OpenDay(ctx context.Context, branch BranchRef) (FiscalDaySession, error)
    RegisterSale(ctx context.Context, session FiscalDaySession, sale FiscalSale) (FiscalReceipt, error)
    VoidSale(ctx context.Context, session FiscalDaySession, receipt FiscalReceipt) error
    CloseDay(ctx context.Context, session FiscalDaySession) (ZReport, error)
    GetStatus(ctx context.Context) (DeviceStatus, error)
}

type FiscalDaySession struct {
    SessionID          string
    DeviceID           string
    BranchID           uuid.UUID
    OpenedAt           time.Time
    OpeningCashAmount  decimal.Decimal
}

type FiscalSale struct {
    SaleID       uuid.UUID
    Items        []FiscalSaleItem
    PaymentType  string // cash | card | mixed
    TotalAmount  decimal.Decimal
    TaxBreakdown []FiscalTaxLine
    Customer     *FiscalCustomer // opsiyonel, e-fatura için
}

type FiscalReceipt struct {
    ReceiptNo    string // cihazdan gelen fiş numarası
    FiscalMemory string // cihaz mali hafıza referansı
    PrintedAt    time.Time
    QRCode       string // GIB QR (yasal zorunluluk)
}

type ZReport struct {
    ReportNo    string
    BranchID    uuid.UUID
    DayOpenedAt time.Time
    DayClosedAt time.Time
    TotalSales  decimal.Decimal
    TotalTax    decimal.Decimal
    SaleCount   int
    RawData     []byte // cihaz formatı, denetim için saklanır
}
```

### 2. Event Şemaları (`contracts/events/fiscal/`)

- `fiscal.day.opened.v1`
- `fiscal.sale.registered.v1`
- `fiscal.sale.voided.v1`
- `fiscal.day.closed.v1`
- `fiscal.zreport.generated.v1`

Her event `tenant_id` + `branch_id` + `device_id` taşır.

### 3. Mock Adapter (Faz 0-1)

```go
type MockFiscalAdapter struct {
    // fake receipt numbers, in-memory session state
}
// WithError(ErrDeviceOffline) ile hata simüle edebilir
```

### 4. Şema Değişikliği

```sql
ALTER TABLE branch_settings ADD COLUMN fiscal_device_type TEXT NOT NULL DEFAULT 'none';
-- 'none' | 'mock' | 'hugin' | 'profilo' | 'beko' | 'ingenico_yn' | 'verifone_yn'
ALTER TABLE branch_settings ADD COLUMN fiscal_device_config JSONB NOT NULL DEFAULT '{}';
```

### 5. Payment Modülü Kuralı

Payment modülü **her ödeme işleminde** FiscalAdapter çağırır. `fiscal_device_type = 'none'` ise MockAdapter no-op döner ama interface çağrısı her zaman yapılır:

```go
func (s *PaymentService) ProcessPayment(ctx context.Context, req PaymentRequest) (*Payment, error) {
    // kart/nakit işlemi
    session := s.fiscalSession.GetOrOpen(ctx, req.BranchID)
    receipt, err := s.fiscalAdapter.RegisterSale(ctx, session, buildFiscalSale(req))
    if err != nil { /* handle */ }
    payment.FiscalReceiptNo = receipt.ReceiptNo
    payment.FiscalQRCode = receipt.QRCode
    s.events.Publish("fiscal.sale.registered.v1", ...)
}
```

### 6. Faz 2 Gerçek Adapter'lar

```
internal/modules/payment/fiscal/
├── mock/
├── hugin/
├── profilo/
├── beko/
├── ingenico_yn/
└── verifone_yn/
```

Factory: `branch_settings.fiscal_device_type`'a göre adapter seçimi.

### 7. Temporal Workflow (Faz 3)

Gün sonu kapatma akışı Temporal workflow olarak yazılır (cihaz offline → saatlerce retry-resume).

## Sonuçlar

**İyi:**
- Payment modülü Faz 0'da doğru temelde yazılır; Faz 2'de gerçek cihaz = sadece adapter ekleme.
- Mock adapter ile CI'da fiscal integration test koşabilir.
- Yasal uyum roadmap'i net.

**Dikkat:**
- `fiscal_device_type = 'none'` ile üretim satışı yasal değildir. Admin panel validation ile zorlanır.
- Gerçek cihaz çağrısı Payment hot path'ine 100-500ms ekler. Timeout + retry adapter-specific.

**Risk:**
- GIB yeni TSM protokolü çıkarırsa interface v2 gerekebilir. Faz 2'de backward compat planı yapılmalı.
- SDK lisansları ücretli olabilir. Faz 2 bütçesine yazılmalı.

## Değerlendirilen Alternatifler

- **Faz 2'de interface + implementation birlikte:** Reddedildi. Payment modülü fiscal-aware yazılmazsa yeniden yazım.
- **Direct SDK integration (interface yok):** Reddedildi. Marka değişimi = tüm Payment kodunu değiştirmek.
- **Harici fiscal proxy servisi:** Değerlendirilecek Faz 3+. PCI/mali scope daraltma için.
