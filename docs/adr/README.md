# ADR Dizini — Online Menu

Mimari Karar Kayıtları (Architecture Decision Records). Her karar kendi dosyasında, değişmez biçimde saklanır.

## Kurallar

- ADR **asla silinmez**. Sadece `Durum: Değiştirildi` veya `Durum: Reddedildi`ye çevrilir.
- Kararın değişmesi = eski ADR `Durum: Değiştirildi`, yeni ADR (ör: `SEC-001-v2`) oluşturulur.
- Taslak (📝) ADR'lar ilgili delta implementasyonu başlarken "Kabul Edildi"ye çevrilir.

## ADR İndeksi

### Güvenlik (SEC)
| Kod | Başlık | Durum |
|---|---|---|
| [SEC-001](SEC-001-rls-transaction-scoped.md) | RLS İçin Transaction-Scoped Tenant İzolasyonu | ✅ Kabul Edildi |
| [SEC-002](SEC-002-rls-force-runtime-role.md) | RLS FORCE ve Ayrı Runtime Rolü | ✅ Kabul Edildi |
| [SEC-003](SEC-003-idempotency-key.md) | Idempotency-Key Altyapısı | ✅ Kabul Edildi |
| [SEC-004](SEC-004-device-pairing.md) | Cihaz Kayıt ve Pairing Code Akışı | 📝 Taslak |

### Yetkilendirme (AUTH)
| Kod | Başlık | Durum |
|---|---|---|
| [AUTH-001](AUTH-001-four-layer-authorization.md) | Dört Katmanlı Authorization Mimarisi | ✅ Kabul Edildi |
| [AUTH-002](AUTH-002-keycloak-single-realm.md) | Keycloak Tek Realm Stratejisi | ✅ Kabul Edildi |

### Veri / Event (DATA)
| Kod | Başlık | Durum |
|---|---|---|
| [DATA-001](DATA-001-outbox-dispatcher.md) | Outbox Dispatcher Mimarisi | ✅ Kabul Edildi |
| [DATA-002](DATA-002-event-immutability.md) | Event Immutability Kuralı | ✅ Kabul Edildi |
| [DATA-003](DATA-003-timezone-business-day.md) | Timezone ve Business Day Hesaplama | 📝 Taslak |
| [DATA-004](DATA-004-catalog-delta-sync.md) | Catalog Delta Sync | 📝 Taslak |

### Mimari (ARCH)
| Kod | Başlık | Durum |
|---|---|---|
| [ARCH-001](ARCH-001-feature-flags.md) | İki Katmanlı Feature Flag | 📝 Taslak |
| [ARCH-002](ARCH-002-asynq-temporal.md) | asynq ve Temporal Sorumluluk Ayrımı | ✅ Kabul Edildi |
| [ARCH-003](ARCH-003-search-typesense.md) | Arama Backend Seçimi (Typesense) | ✅ Kabul Edildi |
| [ARCH-004](ARCH-004-task-runner.md) | Task Runner Seçimi (Taskfile) | ✅ Kabul Edildi |
| [ARCH-005](ARCH-005-frontend-monorepo.md) | Frontend Monorepo Yapısı (pnpm workspaces) | ✅ Kabul Edildi |

### Mali (FISCAL)
| Kod | Başlık | Durum |
|---|---|---|
| [FISCAL-001](FISCAL-001-fiscal-adapter.md) | Fiscal Device Adapter Interface | ✅ Kabul Edildi |

### Operasyonel (OPS)
| Kod | Başlık | Durum |
|---|---|---|
| [OPS-001](OPS-001-backup-dr.md) | Backup ve Disaster Recovery | 📝 Taslak |
| [OPS-002](OPS-002-tenant-offboarding.md) | Tenant Offboarding ve KVKK Uyumu | 📝 Taslak |
| [OPS-003](OPS-003-rate-limiting.md) | Rate Limiting Stratejisi | 📝 Taslak |
| [OPS-004](OPS-004-cost-observability.md) | Cost Observability | 📝 Taslak |

## Sembol Anlamı

- ✅ **Kabul Edildi** — Tam ADR, karar kesinleşmiş, uygulamaya geçilebilir.
- 📝 **Taslak** — İskelet ADR. Ayrıntılar implementasyon sırasında dolacak.
- ❌ **Reddedildi** — Değerlendirildi, hayata geçirilmedi.
- 🔄 **Değiştirildi** — Yerine yeni ADR geldi.
