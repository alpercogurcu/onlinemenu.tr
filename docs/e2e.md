# Admin Rol Bazlı E2E (Playwright)

`web/apps/admin/e2e/` altındaki paket, admin panelini beş dev kullanıcısıyla
(yönetici, shift müdürü, kasiyer, garson, mutfak) gerçek yığına karşı gezer.

## Ön koşul

Yığın Playwright'ın dışında ayakta olmalı — test koşucusu sunucu başlatmaz:

1. `task compose:up:core` (Postgres :5442, Redis :6389, NATS, Keycloak)
2. `task backend:migrate:up` ve `backend/deploy/dev-seed.sql` (rol kullanıcıları
   `shift@/kasiyer@/garson@/mutfak@dev.onlinemenu.tr` bu seed'den gelir)
3. API `:8081`, `APP_ENV=dev` (dev login açık)
4. Admin dev server `:3000` (`task web:dev`)

## Koşturma

```bash
task web:e2e                       # :3000'e karşı
E2E_BASE_URL=http://localhost:3002 task web:e2e   # başka port
E2E_API_URL=http://localhost:8082 task web:e2e    # API başka portta
```

İlk kurulumda tarayıcı gerekir: `pnpm exec playwright install chromium`
(`web/apps/admin` içinde).

## Senaryolar

| Dosya | Ne doğrular |
|---|---|
| `roles.spec.ts` | Rapor yalnız shift/yönetici; kasiyer ürün ekleyemez (403 toast); garson masaları görür, QR devre dışı, katalog 403; mutfak KDS "Canlı", kullanıcı listesi kapalı |
| `catalog.spec.ts` | Ürün → grup → 3 seçenek (Enter zinciri) → sil; katalog başladığı gibi kalır |
| `kds.spec.ts` | API ile sipariş; mutfak "Kasa onayı bekleniyor" görür, kasa kabulünden sonra "Hazırlamaya Başla" → "Hazır"; cihaz koyu modu yalnız `[data-kds-root]` |
| `theme.spec.ts` | Koyu tema `html.dark`, yenilemede kalır (`onlinemenu-theme`); KDS dışında yerel `.dark` yok |

Kurallar: CTX token bellekte durduğundan sayfalar arası geçiş `gotoSpa` (SPA
router) ile yapılır; testler paralel koşmaz (tek tenant, tek KDS akışı); her
senaryo kendi verisini üretir ve siler.
