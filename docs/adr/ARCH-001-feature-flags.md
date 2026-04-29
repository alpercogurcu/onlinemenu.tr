# ADR-ARCH-001: İki Katmanlı Feature Flag

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P1-2
**Kategori:** Mimari (ARCH)

## Bağlam

Baseline'da iki yerde flag mantığı:
1. `tenants.enabled_modules JSONB` — billing/entitlement kararı
2. Unleash/GrowthBook — Faz 2, operasyonel flag

İkisi aynı araçla yönetilirse karışıklık olur: "bu flag kapalı, müşteri satın almadı mı, yoksa biz mi yavaş rollout yapıyoruz?" sorusu cevapsız kalır.

## Karar

### Katman A — Billing / Entitlement (Faz 0)

- `tenants.enabled_modules JSONB` — hangi modüller satın alındı
- Platform helper: `ModuleGate(ctx, moduleName) bool`
- Modül kayıt anında kontrol: flag kapalıysa HTTP route register edilmez, event subscribe edilmez.
- **Migration her zaman çalışır** (modül aktif olmasa bile veri bütünlüğü için).
- Değişiklik: admin paneli → tenant update → Redis cache invalidate + NATS broadcast.

### Katman B — Operasyonel Rollout (Faz 2+)

- **Unleash** kullanılır (ADR-ARCH-003 kararı)
- Canary, gradual percentage, tenant/şube/plan context-aware evaluation
- `FeatureFlag(ctx, flagName, defaultValue) bool` helper

### Kesin Kural

**Unleash entitlement için KULLANILMAZ.** Entitlement = revenue-driving, audit-required, customer-facing. Unleash = dev/ops kararı, hızlı değişebilir.

### Module Registry

```go
type Module interface {
    Name() string
    RegisterRoutes(r chi.Router)
    RegisterEventHandlers(bus eventbus.Subscriber) error
    Migrate(ctx context.Context, pool *pgxpool.Pool) error
}

// Kayıt döngüsünde gate kontrolü
for _, mod := range modules {
    mod.Migrate(ctx, pool) // her zaman
    if !moduleGate.IsEnabled(ctx, mod.Name()) {
        continue // route ve event subscribe'ı atla
    }
    mod.RegisterRoutes(router)
    mod.RegisterEventHandlers(bus)
}
```

## İmplementasyon Detayları (Dolacak)

- Unleash Go SDK entegrasyonu (`Unleash/unleash-client-go`)
- Cache stratejisi (TTL + invalidation event)
- Admin panel UI'ında iki katmanın görsel ayrımı ("Satın alınan modüller" vs "Aktif özellikler")

## Değerlendirilen Alternatifler

- **Tek araç (Unleash her şey için):** Reddedildi. Entitlement Unleash'e bağlanırsa audit trail karmaşıklaşır, gelir doğrulama zorlaşır.
- **GrowthBook:** Reddedildi. A/B test ve analitik odaklı, POS için overkill. Unleash daha saf feature flag aracı.
- **DB tablosu (her flag bir satır):** Değerlendirildi. Entitlement için zaten JSONB var; ayrı tablo ek karmaşıklık.
