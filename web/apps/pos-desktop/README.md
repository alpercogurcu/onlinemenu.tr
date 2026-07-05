# pos-desktop

Wails v2 + React (TypeScript) POS istasyon istemcisi. Bu doküman bir
**temel (foundation) iskelet**i tanımlar — gerçek POS ekranları (masa planı,
adisyon, ödeme) sonraki UI dalgasında eklenir. Bkz. `docs/lessons-from-b2b.md`
Bölüm 5 — buradaki her kural o denetimden aktarılan bir regresyonun tekrar
etmemesi için var ve prose değil, kod/test/lint ile zorlanıyor.

## Mimari: Tek token-refresh otoritesi (Go)

**Webview hiçbir zaman HTTP çağrısı yapmaz.** Backend ile konuşan, token
saklayan ve token yenileyen **tek** kod parçası `internal/apiclient.Client`
(Go). Frontend yalnızca Wails binding'leri üzerinden `App` struct'ının
exported metotlarını çağırır (`frontend/wailsjs/go/main/App` — otomatik
üretilir, bkz. aşağıda).

```
┌─────────────────────┐   Wails binding    ┌──────────────────┐   HTTP    ┌─────────────┐
│ React webview        │ ─────────────────▶ │ App (main.go)     │ ────────▶ │ backend API │
│ (fetch/axios YASAK)  │                    │  -> apiclient.Client│          │ cmd/api      │
└─────────────────────┘                    └──────────────────┘          └─────────────┘
                                                     │
                                                     ▼
                                            internal/tokenstore
                                            (OS keychain / 0600 fallback)
```

Bu sınır stilistik değil, yapısaldır: `frontend/eslint.config.mjs` bu app'te
`fetch`, `XMLHttpRequest`, `axios`, `node-fetch` kullanımını **lint hatası**
olarak zorlar (b2b'de aynı kural yalnızca CLAUDE.md'de yazıyordu ve iki
bağımsız interceptor aynı config dosyasına yarışıyordu — burada ikinci bir
interceptor'ın var olabileceği kod yolu yok).

## Token saklama

`internal/tokenstore` — OS keychain (macOS Keychain / Windows Credential
Manager / Linux Secret Service, `github.com/zalando/go-keyring` üzerinden)
birincil depo. Keychain gerçek bir round-trip (write→read→delete) ile
prob edilir; başarısız olursa 0600 dosya fallback'e düşülür ve
`runtime.LogWarning` ile **açıkça** uyarı basılır — sessiz düşüş yok.

## Donanım soyutlaması

`internal/hardware` — `Device` arayüzü (`Kind()`, `Status()`, `Events()`).
`MockPrinter` gerçek donanım olmadan geliştirmeyi mümkün kılan referans
implementasyon. Kritik desen: **her durum geçişi (bağlantı, kopma, hata)
açık bir `Event` olarak `Events()` kanalına gönderilir** — b2b'deki terazi
regresyonunda kopan bağlantı sonsuza dek "bağlı" görünüyordu çünkü poll
döngüsü hatayı yutup son bilinen durumu tekrarlıyordu. Burada:

- `StatusDisconnected` zero-value'dur (başlatılmamış cihaz asla "bağlı" okunmaz).
- Fault → `StatusError` geçişi her zaman tetikleyen `error` ile birlikte gelir.
- Event loop goroutine'i `context.Context` ile iptal edilebilir, `sync.WaitGroup`
  ile izlenir (`Wait()`), kanal kapanışı goroutine çıkışına garantili sırayla bağlı.
  `internal/hardware/mock_printer_test.go` bunu `go.uber.org/goleak` ile doğrular.

Gerçek yazıcı/terazi/fiscal adaptörleri UI dalgasında aynı `Device`
arayüzünü implemente edecek; forwarding deseni (Go event kanalı →
`runtime.EventsEmit`, bkz. `app.go:startHardware`) değişmeyecek.

## Devtools / Inspector — yalnızca dev build

`main.go`'daki `Debug.OpenInspectorOnStartup` alanı `devtools_dev.go`
(`//go:build dev`, `true`) / `devtools_release.go` (`//go:build !dev`,
`false`) arasında build-tag ile sabitlenir. `dev` etiketi kasıtlı olarak
Wails'in **kendi** iç `dev`-mode etiketiyle aynıdır (`wails dev` bunu
otomatik ekler; bkz. "Bilinen sorunlar"), böylece `wails dev` her zaman
inspector'ı açık derler, `wails build` (etiketsiz) her zaman kapalı derler.
Release binary'sinde inspector'ın asla açılamayacağını garanti eden şey
budur — build config'i değil.

## Config

`internal/config.Load(dataDir)` öncelik sırası:
1. `POS_API_BASE_URL` ortam değişkeni (dev kolaylığı)
2. `<dataDir>/config.json` (`{"api_base_url": "..."}`)
3. Varsayılan: `http://localhost:8080`

`dataDir` çalışma zamanında `os.UserConfigDir()/onlinemenu-pos-desktop`
olarak çözülür (macOS: `~/Library/Application Support/...`).

## Backend'e bağlanma (dev)

Backend'in `APP_ENV=dev` ile çalıştığı varsayılır (`task compose:up` +
`task backend:dev`). Login akışı `POST /dev/login` (dev-only, gerçek
Keycloak akışının yerini tutan placeholder) → dönen context token
`tokenstore`'a yazılır → `GET /v1/identity/me` ile doğrulanır (`WhoAmI`).
Gerçek Keycloak login'i UI dalgasında bu ikisinin yerini alacak;
`apiclient.Client` arayüzü (Login/WhoAmI/Ping/Logout) değişmeden kalabilir.

## Wails binding'leri (şu an)

`frontend/wailsjs/go/main/App` içinde üretilir (bkz. "Kod üretimi"):

| Metot | İmza | Açıklama |
|---|---|---|
| `Login` | `(email: string) => Promise<SessionDTO>` | `POST /dev/login`, token'ı persist eder |
| `WhoAmI` | `() => Promise<SessionDTO>` | `GET /v1/identity/me`, oturumu doğrular |
| `Logout` | `() => Promise<void>` | token'ı temizler |
| `Ping` | `() => Promise<void>` | `GET /healthz`, kimlik gerektirmez |

UI dalgası bu listeyi `GetChecks`, `PlaceOrder` vb. ile genişletecek — desen
aynı kalır: her yeni backend etkileşimi `App`'e bir metot olarak eklenir,
`internal/apiclient.Client`'a karşılık gelen bir metot delege edilir.

## Kod üretimi (wailsjs) — fresh clone'da ZORUNLU ilk adım

`frontend/wailsjs/` **committed değildir** (`.gitignore`) — bu repodaki diğer
codegen çıktıları gibi (bkz. `packages/types`, backend `contracts/openapi/`),
üretilen kod git'e girmez, kaynaktan (App struct'ının exported metotları)
yeniden üretilir. `App`'in imzası her değiştiğinde ya da **taze bir clone'da**
şununla üretilir:

```
task pos:generate     # = wails generate module
```

Bunu atlarsan `pnpm --filter @onlinemenu/pos-desktop typecheck|lint|build`
`../wailsjs/go/main/App` bulunamadı hatasıyla başarısız olur — bu beklenen
davranıştır, sessiz bir kırılma değildir. `wails dev` / `wails build` zaten
kendi içinde `wails generate module`'ü otomatik çalıştırır; bu adım yalnızca
Wails CLI'ı çağırmadan salt frontend tooling (`pnpm typecheck`/`lint`) çalıştırmak
istediğinde gerekir.

`go build ./...` bundan etkilenmez — Go tarafı yalnızca
`frontend/web-build` (derlenmiş statik varlıklar) embed eder, `wailsjs`'e
(TypeScript binding'leri) bağımlı değildir. `frontend/web-build/.gitkeep`
committed'dir, bu yüzden `go build` taze bir clone'da da hiçbir ön adım
gerekmeden derlenir. Vite'ın çıktı dizini kasıtlı olarak `dist` değil
`web-build` (bkz. `vite.config.ts`) — repo kökü `.gitignore`'ı `dist/`
adında her dizini genel olarak yok sayıyor ve bir üst dizin kalıbı bir
dizini tamamen dışladığında iç içe `.gitignore` negation'ı ile geri dahil
etmek mümkün değil (git'in belgelenmiş sınırlaması). `web-build/.gitkeep`
committed, gerçek derlenmiş varlıklar yok sayılır.

Not: Vite varsayılan olarak `outDir`'i her build'de temizler
(`emptyOutDir`), yani yerelde `wails build`/`vite build` çalıştırdığında
`.gitkeep` diskten silinir — bu zararsızdır (git'teki committed kopya
etkilenmez; yeni bir `git clone` her zaman ilk build'den ÖNCE `.gitkeep`
ile başlar, garanti ettiği tek şey bu). Eğer commit edilmeden önce bu
dosyayı yerelde yeniden oluşturman gerekirse: `touch frontend/web-build/.gitkeep`.

## pnpm workspace notu

Wails'in standart yerleşimi Go modülünü `apps/pos-desktop/` köküne, frontend
paketini `apps/pos-desktop/frontend/` altına koyar. `web/pnpm-workspace.yaml`
bu nedenle `apps/pos-desktop/frontend`'i **açıkça** listeler — `apps/*` glob'u
yalnızca doğrudan alt dizinleri eşler, `frontend/` bunun bir seviye altında.
`wails.json`'daki `frontend:install`/`frontend:build`/`frontend:dev:watcher`
komutları `npm` değil `pnpm` çalıştırır (workspace'e dahil olmak için).

`@onlinemenu/ui-kit` şu an yalnızca `export {}` içeriyor (henüz shadcn
bileşeni yok) — bu app ondan import **denemedi**; Tailwind v4 + düz CSS ile
minimal bir temel kuruldu (bkz. `frontend/src/style.css`,
`frontend/postcss.config.mjs`, admin'deki kurulumla aynı desen). UI dalgası
ui-kit'e bileşen ekledikçe bu app oradan import etmeye başlayabilir.

## Geliştirme

```
task pos:dev     # wails dev (hot reload)
task pos:build   # wails build (production .app / .exe)
task pos:test    # go build + go vet + go test -race -cover
```

## Bilinen sorunlar

- **`wails dev` / `-tags dev` link hatası (bu makinede, Xcode 16 / macOS 14.6.1
  / Wails v2.11.0):** Wails'in dev-mode asset serving kodu (`-tags dev`,
  Wails'in kendi iç kullanımı) `UTType` sembolüne referans veriyor ama
  `UniformTypeIdentifiers` framework'ünü linklemiyor — linker hatası verir.
  **Bu, bu scaffold'un koduyla ilgisiz**: hiçbir değişiklik yapılmamış saf
  `wails init -t react-ts` şablonunda da aynı hata reprodüklendi. Workaround
  `task pos:dev` içine gömüldü: `CGO_LDFLAGS="-framework UniformTypeIdentifiers"`
  darwin'de otomatik export edilir. `wails build` (release, `-tags dev`
  kullanmaz) bu sorundan etkilenmez — bu repo'da tam olarak doğrulandı
  (`.app` bundle üretildi, bkz. rapor).
