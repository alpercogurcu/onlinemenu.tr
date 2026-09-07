# Deploy Öncesi Açıklar + Form Dialog'ları — Uygulama Planı

> **For agentic workers:** superpowers:subagent-driven-development ile görev görev uygulanır. Adımlar `- [ ]` ile izlenir.

**Hedef:** `docs/backlog-pilot.md`'deki deploy-öncesi açıkları kapatmak (personel onboarding e-posta senkronu, davette şube doğrulaması, Alertmanager + prod Prometheus ayrımı) ve admin panelde sağdan açılan Sheet form'larını ortalanmış kart tipi Dialog'a çevirmek.

**Mimari:** Backend'de identity modülü tenant/public `TenantReader` üzerinden şube varlığını doğrular (FK yok, modül izolasyonu korunur); Keycloak e-posta değişimi davet sırasında persons'a geri yazılır (Keycloak kaynak). Deploy'da Alertmanager observability profiline eklenir, prod Prometheus dosyası ayrılır. Frontend'de tek bir `FormDialog` sarmalayıcı (Dialog primitifi üstünde) tüm form ekranlarına uygulanır.

**Spec:** `docs/backlog-pilot.md` §1-2 ve kullanıcı isteği (2026-09-07): "sağdaki drawer'lar dialog/kart olsun; drawer'da içerikle genişlik sıfıra sıfır, boşluk yok". Tasarım: canvas artboard 4 (https://claude.ai/code/artifact/f74fd0ae-67b2-4268-9c4f-f9a3455fcdc5).

## Global Constraints
- Kod/yorum İngilizce; commit Türkçe `tip(alan): açıklama`; AI attribution footer'ı YOK; push yok.
- Modül izolasyonu: başka modülden yalnız `public/` import; go-arch-lint `.go-arch-lint.yml` güncellenmeden geçmez.
- DB erişimi yalnız `WithTenantTx/WithTenantReadTx/WithAllTenantsTx`.
- `.env` düz metin commit edilmez; sırlar dosya/secret ile.
- Admin: shadcn primitifleri, token sınıfları (ham palet yasak — `no-raw-palette.test.ts`), vitest + RTL; Playwright yalnız controller koşturur.
- Dialog: her form ekranında aynı sarmalayıcı, `Escape`/dışarı tıklama ile kapanır ama kaydetme sürerken kapanmaz; tüm alanlar `Label htmlFor`; mobilde tam genişlik (`max-w-[calc(100%-2rem)]`), gövde `max-h-[70vh] overflow-y-auto`.

### Rulings
- R1: Keycloak e-posta kaynaktır. Davette e-posta ile `persons` satırı bulunur ve o kişinin `keycloak_sub`'ı Keycloak'ta varsa yeni Keycloak kullanıcısı YARATILMAZ; kişinin e-postası Keycloak'taki güncel değere çekilir, yanıtta `person.email` bunu yansıtır. Kişi yok ama Keycloak'ta kullanıcı varsa mevcut akış.
- R2: Şube doğrulaması `pub.TenantReader.GetBranch` ile; bulunamazsa 422 (`ErrInvalidInput`). `MembershipService.Create` ve `StaffInviteService.Invite` ikisi de doğrular.
- R3: Alertmanager e-posta alıcısı: SMTP_* değerleri Keycloak için zaten zorunlu; şifre `smtp_auth_password_file` ile git-dışı dosyadan. Alıcı adresi ve SMTP host/port `deploy/alertmanager/alertmanager.yml`'de kurulumda düzenlenir (Alertmanager env genişletmez). Webhook alıcısı opsiyonel, yorumlu.
- R4: Sheet tamamen kaldırılmaz (sidebar mobil menüsü kullanıyor); form ekranlarında `FormDialog` kullanılır. `menu-items-sheet.tsx` → `menu-items-dialog.tsx` (geniş, `sm:max-w-2xl`).

---

### Task 1: Backend — davette e-posta senkronu ve şube doğrulaması
**Files:** `backend/internal/platform/keycloak/client.go` (+`GetUserByID` AdminAPI'ye), `backend/internal/modules/identity/repo/person_repo.go` (+`GetByEmail`, platform-scope), `backend/internal/modules/identity/service/staff_invite_service.go`, `membership_service.go`, `backend/internal/modules/identity/module.go`, `backend/internal/modules/tenant/module.go` (`fx.As(new(pub.TenantReader))`), `backend/.go-arch-lint.yml` (identity_service → +tenant_public), testler (`staff_invite_service_test.go` fake admin + fake TenantReader; repo integration test `GetByEmail`).
- [ ] Testler → uygulama → `go test ./internal/modules/identity/... ./internal/modules/tenant/... -race` + `task backend:lint` → commit'ler `fix(identity): davette Keycloak e-postası persons'a senkron`, `fix(identity): üyelik ve davette şube varlığı doğrulanıyor`.

### Task 2: Deploy — Alertmanager ve prod Prometheus
**Files:** `deploy/alertmanager/alertmanager.yml` (yeni), `deploy/alertmanager/.gitignore` (`smtp_password`), `deploy/prometheus/prometheus.prod.yml` (yeni; `external_labels cluster: prod, environment: production`, `alerting.alertmanagers`, prod scrape hedefleri compose servis adlarıyla), `deploy/docker-compose.prod.yml` (alertmanager servisi observability profili; prometheus prod yml mount + `--web.enable-lifecycle` yok), `deploy/.env.prod.example` (yorum: alertmanager alıcı adresi), `docs/backlog-pilot.md` (kapanan maddeler silinir), `deploy/smoke.sh` (alertmanager `/-/ready` opsiyonel kontrol, profil açıksa).
- [ ] `docker compose -f deploy/docker-compose.prod.yml config` ile doğrulama; `promtool check config` imaj içinden (`docker run --rm -v ... prom/prometheus:v3.3.1 promtool check config`) ve `amtool check-config`. Commit `feat(deploy): Alertmanager e-posta alıcısı ve prod Prometheus yapılandırması`.

### Task 3: Admin — FormDialog ve Sheet dönüşümü
**Files:** `web/apps/admin/src/components/layouts/form-dialog.tsx` (yeni), 11 sayfa (`settings/users`, `settings/branches`, `settings/fiscal-terminals`, `catalog/menus`, `catalog/categories`, `inventory/warehouses`, `inventory/stock-items`, `inventory/supply-policies`, `inventory/purchase-receipts`, `parties/page`, `parties/customers`), `components/catalog/menu-items-sheet.tsx` → `menu-items-dialog.tsx` (+ test dosyası yeniden adlandırma), `src/test/form-dialog.test.tsx` (yeni), mevcut testler (`users-page.test.tsx`, `menu-items-sheet.test.tsx`).
- `FormDialog` props: `open, onOpenChange, title, description?, size?: "md"|"lg"|"xl"` (`sm:max-w-lg` / `sm:max-w-xl` / `sm:max-w-2xl`), `footer?: ReactNode`, `busy?: boolean` (busy iken Escape/dışarı tıklama kapatmaz, X devre dışı), `children`. Gövde: `max-h-[70vh] overflow-y-auto px-6 py-4`; başlık/açıklama üstte, footer sağa yaslı (Vazgeç outline + birincil).
- Form'lardaki tam genişlik `w-full` "Kaydet" düğmeleri footer'a taşınır (`form` id + `<Button form="…" type="submit">`), gövde içinde kalmaz.
- [ ] Testler → uygulama → `pnpm exec tsc --noEmit && pnpm exec vitest run && pnpm exec eslint .` → commit'ler `feat(admin): FormDialog sarmalayıcısı`, `refactor(admin): form ekranları sağ çekmeceden kart dialog'a`.

### Task 4: Doğrulama (controller)
- [ ] Playwright e2e, tarayıcı turu (dialog'lar), backlog-pilot güncelleme, merge.
