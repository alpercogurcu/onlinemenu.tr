# Monorepo Yeniden Yapılandırma Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go backend'i `backend/` altına taşımak, manufacturing ve notification stub modüllerini eklemek, POS/core/finance deployment grupları için ayrı `cmd/` binary iskeletleri oluşturmak.

**Architecture:** Tek `go.mod` (`module onlinemenu.tr`) `backend/go.mod`'a taşınır; module adı değişmediği için tüm import path'ler (`onlinemenu.tr/internal/...`) geçerli kalır. Üç yeni `cmd/` binary uber-go/fx ile yalnızca ilgili modül grubunu yükler. Root `Taskfile.yml` includes yapısıyla `backend/`, `web/` ve `mobile/` alt Taskfile'larını birleştirir.

**Tech Stack:** Go 1.24, uber-go/fx, chi v5, go-arch-lint, golangci-lint, air, GitHub Actions, Taskfile v3

---

### Task 1: Go kaynaklarını `backend/` altına taşı

**Files:**
- Move: `cmd/` → `backend/cmd/`
- Move: `internal/` → `backend/internal/`
- Move: `migrations/` → `backend/migrations/`
- Move: `contracts/` → `backend/contracts/`
- Move: `configs/` → `backend/configs/`
- Move: `bin/` → `backend/bin/`
- Move: `go.mod` → `backend/go.mod`
- Move: `go.sum` → `backend/go.sum`

- [ ] **Adım 1: `backend/` dizinini oluştur**

```bash
mkdir -p backend
```

Expected: çıktı yok

- [ ] **Adım 2: Kaynak klasörlerini taşı**

```bash
git mv cmd backend/cmd
git mv internal backend/internal
git mv migrations backend/migrations
git mv contracts backend/contracts
git mv configs backend/configs
git mv bin backend/bin
```

Expected: çıktı yok (git mv sessiz çalışır)

- [ ] **Adım 3: Go module dosyalarını taşı**

```bash
git mv go.mod backend/go.mod
git mv go.sum backend/go.sum
```

Expected: çıktı yok

- [ ] **Adım 4: `backend/go.mod` içindeki module adının değişmediğini doğrula**

```bash
head -1 backend/go.mod
```

Expected: `module onlinemenu.tr` — bu satır değişmemiş olmalı

- [ ] **Adım 5: `backend/` içinden derleme doğrula**

```bash
cd backend && go build ./... && cd ..
```

Expected: hata yok (import path'ler değişmedi)

- [ ] **Adım 6: Commit**

```bash
git add -A
git commit -m "refactor: Go kaynaklarını backend/ altına taşı"
```

---

### Task 2: Tooling config dosyalarını `backend/` altına taşı

**Files:**
- Move: `.air.toml` → `backend/.air.toml`
- Move: `.go-arch-lint.yml` → `backend/.go-arch-lint.yml`
- Move: `.golangci.yml` → `backend/.golangci.yml`
- Move: `.gosec.yml` → `backend/.gosec.yml` (varsa)
- Move: `sqlc.yaml` veya `sqlc.yml` → `backend/` (varsa)

- [ ] **Adım 1: Tooling dosyalarını taşı**

```bash
git mv .air.toml backend/.air.toml
git mv .go-arch-lint.yml backend/.go-arch-lint.yml
git mv .golangci.yml backend/.golangci.yml
[ -f .gosec.yml ] && git mv .gosec.yml backend/.gosec.yml || echo "skip: .gosec.yml yok"
[ -f sqlc.yaml ] && git mv sqlc.yaml backend/sqlc.yaml || echo "skip: sqlc.yaml yok"
[ -f sqlc.yml ] && git mv sqlc.yml backend/sqlc.yml || echo "skip: sqlc.yml yok"
```

- [ ] **Adım 2: `backend/.air.toml` path'lerini doğrula — değişiklik gerekmiyor**

```bash
cat backend/.air.toml
```

Expected: `cmd = "go build -o ./tmp/api ./cmd/api"` satırı var; `backend/` içinden çalıştığı için göreli path'ler geçerli.

- [ ] **Adım 3: `go-arch-lint`'in `backend/` kökünden çalışıp çalışmadığını doğrula**

```bash
cd backend && go-arch-lint check && cd ..
```

Expected: lint uyarısı yoksa başarılı; `.go-arch-lint.yml` `backend/` içinden göreli path'lerle çalışır.

- [ ] **Adım 4: `golangci-lint`'i doğrula**

```bash
cd backend && golangci-lint run && cd ..
```

Expected: lint hatasız geçer

- [ ] **Adım 5: Commit**

```bash
git add -A
git commit -m "refactor: tooling config dosyalarını backend/ altına taşı"
```

---

### Task 3: Root `Taskfile.yml`'i böl — includes yapısı

**Files:**
- Modify: `Taskfile.yml` (root) — includes + compose + web görevleri
- Create: `backend/Taskfile.yml` — tüm Go görevleri

- [ ] **Adım 1: `backend/Taskfile.yml` oluştur (mevcut Go görevlerini içerir)**

`backend/Taskfile.yml` dosyasını oluştur:

```yaml
version: '3'

vars:
  GO_MODULE: onlinemenu.tr

tasks:
  dev:
    desc: "air ile hot-reload API (compose zaten ayakta olmalı)"
    cmds:
      - air -c .air.toml

  build:
    desc: "Tüm Go binary'lerini build et (ko ile)"
    cmds:
      - ko build ./cmd/...

  build:local:
    desc: "Lokal Go build (ko olmadan)"
    cmds:
      - go build -o bin/api ./cmd/api
      - go build -o bin/edge ./cmd/edge
      - go build -o bin/worker ./cmd/worker

  test:
    desc: "Tüm testler (race detector açık)"
    cmds:
      - go test ./... -race -count=1

  test:unit:
    desc: "Sadece unit testler"
    cmds:
      - go test ./... -race -short

  test:integration:
    desc: "testcontainers integration testler"
    cmds:
      - go test ./... -race -run Integration -timeout 10m

  test:rls-leak:
    desc: "RLS sızıntı testleri (SEC-001, SEC-002)"
    cmds:
      - go test ./internal/platform/db/... -run TestRLS -v

  test:coverage:
    desc: "Test coverage raporu"
    cmds:
      - go test ./... -race -coverprofile=coverage.out
      - go tool cover -html=coverage.out -o coverage.html

  lint:
    desc: "golangci-lint + go-arch-lint"
    cmds:
      - golangci-lint run
      - go-arch-lint check

  fmt:
    desc: "Go kod formatla"
    cmds:
      - gofmt -w .
      - goimports -w .

  vet:
    desc: "go vet"
    cmds:
      - go vet ./...

  security:scan:
    desc: "gosec + trivy güvenlik taraması"
    cmds:
      - gosec -conf .gosec.yml -exclude-generated ./...
      - trivy fs --severity HIGH,CRITICAL .

  gen:
    desc: "Tüm kod üretimi (sqlc, events)"
    deps: [gen:sqlc, gen:events]

  gen:sqlc:
    desc: "sqlc ile DB kodu üret"
    cmds:
      - sqlc generate

  gen:events:
    desc: "Event JSON Schema → Go struct"
    cmds:
      - go run ./cmd/eventgen contracts/events internal/platform/events/generated

  migrate:up:
    desc: "Tüm modül migration'larını çalıştır"
    cmds:
      - go run ./cmd/migrate up

  migrate:down:
    desc: "Son migration'ı geri al"
    cmds:
      - go run ./cmd/migrate down 1

  migrate:new:
    desc: "Yeni migration oluştur (task migrate:new MODULE=pos NAME=add_table)"
    cmds:
      - migrate create -ext sql -dir migrations/{{.MODULE}} {{.NAME}}

  migrate:verify:
    desc: "RLS policy sayısını doğrula"
    cmds:
      - go run ./cmd/migrate verify

  sops:decrypt:
    desc: ".env.sops → .env (lokal dev için)"
    cmds:
      - sops -d .env.sops > .env

  sops:encrypt:
    desc: ".env → .env.sops"
    cmds:
      - sops -e .env > .env.sops

  ci:
    desc: "CI pipeline lokalde simüle et"
    cmds:
      - task: lint
      - task: vet
      - task: test
      - task: security:scan
      - task: build:local

  clean:
    desc: "Build artifact'larını sil"
    cmds:
      - rm -rf bin/ tmp/ coverage.out coverage.html
```

- [ ] **Adım 2: Root `Taskfile.yml`'i includes yapısına dönüştür**

`Taskfile.yml` (root) dosyasını tamamen şununla değiştir:

```yaml
version: '3'

includes:
  backend:
    taskfile: ./backend/Taskfile.yml
    dir: ./backend
  web:
    taskfile: ./web/Taskfile.yml
    optional: true
  mobile:
    taskfile: ./mobile/Taskfile.yml
    optional: true

tasks:
  dev:
    desc: "Tüm stack'i başlat (compose + backend hot-reload)"
    deps: [compose:up]
    cmds:
      - task: backend:dev

  compose:up:
    desc: "docker-compose.dev.yml'deki tüm servisleri başlat"
    cmds:
      - docker compose -f deploy/docker-compose.dev.yml --profile core --profile auth --profile storage --profile observability up -d

  compose:up:core:
    desc: "Yalnızca core servisleri başlat (postgres, redis, nats, vault)"
    cmds:
      - docker compose -f deploy/docker-compose.dev.yml --profile core up -d

  compose:down:
    desc: "Dev servislerini durdur"
    cmds:
      - docker compose -f deploy/docker-compose.dev.yml down

  compose:logs:
    desc: "Dev servis loglarını izle"
    cmds:
      - docker compose -f deploy/docker-compose.dev.yml logs -f

  web:dev:
    desc: "Admin paneli dev server (Next.js)"
    dir: web/apps/admin
    cmds:
      - pnpm dev

  web:build:
    desc: "Admin paneli production build"
    dir: web
    cmds:
      - pnpm build

  web:install:
    desc: "Frontend bağımlılıklarını kur"
    dir: web
    cmds:
      - pnpm install

  pos:dev:
    desc: "POS desktop dev (Wails)"
    dir: web/apps/pos-desktop
    cmds:
      - wails dev

  pos:build:
    desc: "POS desktop production build"
    dir: web/apps/pos-desktop
    cmds:
      - wails build

  ci:
    desc: "CI pipeline lokalde simüle et"
    cmds:
      - task: backend:ci

  clean:
    desc: "Tüm build artifact'larını sil"
    cmds:
      - task: backend:clean
```

- [ ] **Adım 3: Görev listesini doğrula**

```bash
task --list
```

Expected: `backend:dev`, `backend:test`, `backend:lint`, `compose:up`, `web:dev` vb. listelenir

- [ ] **Adım 4: `task backend:build:local` ile mevcut binary'leri doğrula**

```bash
task backend:build:local
```

Expected: `bin/api`, `bin/edge`, `bin/worker` derlenir; çıktı yok

- [ ] **Adım 5: Commit**

```bash
git add -A
git commit -m "refactor: Taskfile.yml'i includes yapısına böl"
```

---

### Task 4: CI workflow'larını güncelle

**Files:**
- Modify: `.github/workflows/ci.yml` — Go job'larına `working-directory: backend` ekle
- Modify: `.github/workflows/migration-check.yml` — `working-directory: backend` ekle

- [ ] **Adım 1: `ci.yml`'deki Go job adımlarını güncelle**

`.github/workflows/ci.yml` dosyasını aç. Aşağıdaki job'lardaki her Go komutuna `working-directory: backend` ekle:

**`lint` job'u** — `go-arch-lint check` adımına:
```yaml
      - name: go-arch-lint check
        working-directory: backend
        run: go-arch-lint check
```

**`lint` job'u** — `golangci-lint-action`'a:
```yaml
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: latest
          working-directory: backend
```

**`security` job'u** — `gosec` adımına:
```yaml
      - name: Run gosec
        working-directory: backend
        run: gosec -conf .gosec.yml -exclude-generated ./...
```

**`test` job'u** — test adımlarına:
```yaml
      - name: Run tests
        working-directory: backend
        run: go test -race -count=1 -timeout=10m ./...

      - name: Run RLS leak tests
        working-directory: backend
        run: go test -race -run TestRLS -v ./internal/platform/db/...
```

**`build` job'u** — build adımlarına:
```yaml
      - name: Build api
        working-directory: backend
        run: go build -o /dev/null ./cmd/api

      - name: Build edge
        working-directory: backend
        run: go build -o /dev/null ./cmd/edge

      - name: Build worker
        working-directory: backend
        run: go build -o /dev/null ./cmd/worker

      - name: Build migrate
        working-directory: backend
        run: go build -o /dev/null ./cmd/migrate
```

Ayrıca `cache: true` olan `setup-go` adımlarına `cache-dependency-path` ekle:
```yaml
      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
          cache-dependency-path: backend/go.sum
```

- [ ] **Adım 2: `migration-check.yml`'i güncelle**

`.github/workflows/migration-check.yml` dosyasını aç. `paths` tetikleyicisini güncelle:
```yaml
on:
  pull_request:
    paths:
      - 'backend/migrations/**'
```

Migration çalıştırma adımına `working-directory: backend` ekle.

- [ ] **Adım 3: Commit**

```bash
git add .github/workflows/ci.yml .github/workflows/migration-check.yml
git commit -m "ci: CI workflow'larını backend/ çalışma diziniyle güncelle"
```

---

### Task 5: `mobile/` placeholder oluştur

**Files:**
- Create: `mobile/.gitkeep`

- [ ] **Adım 1: Flutter placeholder oluştur**

```bash
mkdir -p mobile
touch mobile/.gitkeep
```

- [ ] **Adım 2: Commit**

```bash
git add mobile/.gitkeep
git commit -m "chore: Flutter için mobile/ placeholder"
```

---

### Task 6: `manufacturing` modül stub'ı oluştur

**Files:**
- Create: `backend/internal/modules/manufacturing/public/public.go`
- Create: `backend/internal/modules/manufacturing/module.go`

- [ ] **Adım 1: Dizin yapısını oluştur**

```bash
mkdir -p backend/internal/modules/manufacturing/public
```

- [ ] **Adım 2: `public/public.go` oluştur**

`backend/internal/modules/manufacturing/public/public.go`:

```go
// Package public exposes the manufacturing module's API surface.
// Other modules may only import this package, never internal sub-packages.
package public

// WorkOrder represents a manufacturing work order visible to other modules.
type WorkOrder struct {
	ID       string
	BranchID string
	Status   WorkOrderStatus
}

// WorkOrderStatus is the lifecycle state of a work order.
type WorkOrderStatus string

const (
	WorkOrderStatusDraft      WorkOrderStatus = "draft"
	WorkOrderStatusInProgress WorkOrderStatus = "in_progress"
	WorkOrderStatusCompleted  WorkOrderStatus = "completed"
	WorkOrderStatusCancelled  WorkOrderStatus = "cancelled"
)

// Service is the interface other modules use to interact with manufacturing.
type Service interface {
	// GetWorkOrder returns a work order by ID within the caller's tenant.
	GetWorkOrder(ctx interface{ Value(any) any }, id string) (*WorkOrder, error)
}
```

- [ ] **Adım 3: `module.go` oluştur**

`backend/internal/modules/manufacturing/module.go`:

```go
// Package manufacturing manages production work orders and bill-of-materials.
package manufacturing

import "go.uber.org/fx"

// Module is the fx module definition for the manufacturing domain.
// Providers and invokers are added as the module is implemented in Faz 1.
var Module = fx.Module("manufacturing")
```

- [ ] **Adım 4: Derlemeyi doğrula**

```bash
cd backend && go build ./internal/modules/manufacturing/... && cd ..
```

Expected: hata yok

- [ ] **Adım 5: Commit**

```bash
git add backend/internal/modules/manufacturing/
git commit -m "feat(manufacturing): modül stub — public interface ve fx.Module"
```

---

### Task 7: `notification` modül stub'ı oluştur

**Files:**
- Create: `backend/internal/modules/notification/public/public.go`
- Create: `backend/internal/modules/notification/module.go`

- [ ] **Adım 1: Dizin yapısını oluştur**

```bash
mkdir -p backend/internal/modules/notification/public
```

- [ ] **Adım 2: `public/public.go` oluştur**

`backend/internal/modules/notification/public/public.go`:

```go
// Package public exposes the notification module's API surface.
// Other modules may only import this package, never internal sub-packages.
package public

// Channel represents the delivery channel for a notification.
type Channel string

const (
	ChannelEmail Channel = "email"
	ChannelSMS   Channel = "sms"
	ChannelPush  Channel = "push"
)

// Message is a notification request sent to one or more recipients.
type Message struct {
	TenantID   string
	Channel    Channel
	Recipients []string
	Subject    string
	Body       string
}

// Sender is the interface other modules use to send notifications.
type Sender interface {
	// Send delivers a notification message. Returns immediately; delivery is async.
	Send(ctx interface{ Value(any) any }, msg Message) error
}
```

- [ ] **Adım 3: `module.go` oluştur**

`backend/internal/modules/notification/module.go`:

```go
// Package notification delivers outbound messages via email, SMS, and push channels.
package notification

import "go.uber.org/fx"

// Module is the fx module definition for the notification domain.
// Providers and invokers are added as the module is implemented in Faz 1.
var Module = fx.Module("notification")
```

- [ ] **Adım 4: Derlemeyi doğrula**

```bash
cd backend && go build ./internal/modules/notification/... && cd ..
```

Expected: hata yok

- [ ] **Adım 5: Commit**

```bash
git add backend/internal/modules/notification/
git commit -m "feat(notification): modül stub — public interface ve fx.Module"
```

---

### Task 8: `cmd/api-core` binary iskeletini oluştur

**Files:**
- Create: `backend/cmd/api-core/main.go`

Bu binary identity + tenant modüllerini yükler. Düşük trafikli, platforma ait tenant ve kimlik operasyonları için.

- [ ] **Adım 1: Dizin oluştur**

```bash
mkdir -p backend/cmd/api-core
```

- [ ] **Adım 2: `main.go` oluştur**

`backend/cmd/api-core/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/identity"
	"onlinemenu.tr/internal/modules/tenant"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/cache"
	"onlinemenu.tr/internal/platform/db"
	"onlinemenu.tr/internal/platform/eventbus"
	platformotel "onlinemenu.tr/internal/platform/otel"
	"onlinemenu.tr/internal/platform/vault"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app := fx.New(
		fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
			return &fxevent.ZapLogger{Logger: log}
		}),

		fx.Provide(newLogger),
		fx.Provide(newDBConfig),
		fx.Provide(newEventBusConfig),
		fx.Provide(newOTelConfig),
		fx.Provide(newVaultConfig),
		fx.Provide(newCacheConfig),
		fx.Provide(newOPAConfig),
		fx.Provide(newHTTPConfig),

		db.Module,
		eventbus.Module,
		auth.Module,
		platformotel.Module,
		vault.Module,
		cache.Module,

		identity.Module,
		tenant.Module,

		fx.Provide(newRouter),
		fx.Invoke(startHTTP),
	)

	app.Run()

	<-ctx.Done()
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := app.Stop(stopCtx); err != nil {
		fmt.Fprintf(os.Stderr, "api-core: graceful shutdown error: %v\n", err)
		os.Exit(1)
	}
}

type httpConfig struct {
	Addr         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

func newRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, "api-core")
	})
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return r
}

func startHTTP(lc fx.Lifecycle, cfg httpConfig, r *chi.Mux, log *zap.Logger) {
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			log.Info("api-core HTTP server starting", zap.String("addr", cfg.Addr))
			go func() {
				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Error("api-core HTTP server error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	})
}

func newLogger() (*zap.Logger, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, fmt.Errorf("api-core: build logger: %w", err)
	}
	return logger, nil
}

func newDBConfig() db.Config {
	return db.Config{
		DSN:             mustEnv("DATABASE_URL"),
		MaxConns:        10,
		MinConns:        2,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}
}

func newEventBusConfig() eventbus.Config {
	return eventbus.Config{
		URL:        mustEnv("NATS_URL"),
		StreamName: "DOMAIN_EVENTS",
		Subjects:   []string{"tenant.>", "identity.>"},
	}
}

func newOTelConfig() platformotel.Config {
	return platformotel.Config{
		Endpoint:       envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		ServiceName:    "onlinemenu-api-core",
		ServiceVersion: envOr("APP_VERSION", "dev"),
	}
}

func newVaultConfig() vault.Config {
	return vault.Config{
		Address: mustEnv("VAULT_ADDR"),
		Token:   mustEnv("VAULT_TOKEN"),
	}
}

func newCacheConfig() cache.Config {
	return cache.Config{
		Addr:     envOr("REDIS_ADDR", "localhost:6379"),
		Password: envOr("REDIS_PASSWORD", ""),
		DB:       0,
		PoolSize: 5,
	}
}

func newOPAConfig() auth.EngineConfig {
	return auth.EngineConfig{
		BundlePath: envOr("OPA_BUNDLE_PATH", "configs/opa/bundles"),
	}
}

func newHTTPConfig() httpConfig {
	return httpConfig{
		Addr:         envOr("HTTP_ADDR", ":8081"),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "api-core: required env var %q is not set\n", key)
		os.Exit(1)
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Adım 3: Derlemeyi doğrula**

```bash
cd backend && go build ./cmd/api-core/... && cd ..
```

Expected: hata yok

- [ ] **Adım 4: Commit**

```bash
git add backend/cmd/api-core/
git commit -m "feat: api-core binary — identity + tenant modülleri"
```

---

### Task 9: `cmd/api-pos` binary iskeletini oluştur

**Files:**
- Create: `backend/cmd/api-pos/main.go`

Bu binary pos, catalog, edge_sync modüllerini yükleyecek. Modüller Faz 1'de implement edildiğinde buraya eklenir. Şimdilik platform + sağlık endpoint'i ile derlenir.

- [ ] **Adım 1: Dizin oluştur**

```bash
mkdir -p backend/cmd/api-pos
```

- [ ] **Adım 2: `main.go` oluştur**

`backend/cmd/api-pos/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/cache"
	"onlinemenu.tr/internal/platform/db"
	"onlinemenu.tr/internal/platform/eventbus"
	platformotel "onlinemenu.tr/internal/platform/otel"
	"onlinemenu.tr/internal/platform/vault"
)

// api-pos serves the POS deployment group: pos, catalog, edge_sync.
// Faz 1: pos.Module, catalog.Module, edge_sync.Module are wired here when implemented.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app := fx.New(
		fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
			return &fxevent.ZapLogger{Logger: log}
		}),

		fx.Provide(newLogger),
		fx.Provide(newDBConfig),
		fx.Provide(newEventBusConfig),
		fx.Provide(newOTelConfig),
		fx.Provide(newVaultConfig),
		fx.Provide(newCacheConfig),
		fx.Provide(newOPAConfig),
		fx.Provide(newHTTPConfig),

		db.Module,
		eventbus.Module,
		auth.Module,
		platformotel.Module,
		vault.Module,
		cache.Module,

		// pos.Module,       (Faz 1)
		// catalog.Module,   (Faz 1)
		// edge_sync.Module, (Faz 1)

		fx.Provide(newRouter),
		fx.Invoke(startHTTP),
	)

	app.Run()

	<-ctx.Done()
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := app.Stop(stopCtx); err != nil {
		fmt.Fprintf(os.Stderr, "api-pos: graceful shutdown error: %v\n", err)
		os.Exit(1)
	}
}

type httpConfig struct {
	Addr         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

func newRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, "api-pos")
	})
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return r
}

func startHTTP(lc fx.Lifecycle, cfg httpConfig, r *chi.Mux, log *zap.Logger) {
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			log.Info("api-pos HTTP server starting", zap.String("addr", cfg.Addr))
			go func() {
				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Error("api-pos HTTP server error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	})
}

func newLogger() (*zap.Logger, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, fmt.Errorf("api-pos: build logger: %w", err)
	}
	return logger, nil
}

func newDBConfig() db.Config {
	return db.Config{
		DSN:             mustEnv("DATABASE_URL"),
		MaxConns:        20,
		MinConns:        4,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}
}

func newEventBusConfig() eventbus.Config {
	return eventbus.Config{
		URL:        mustEnv("NATS_URL"),
		StreamName: "DOMAIN_EVENTS",
		Subjects:   []string{"pos.>", "catalog.>", "inventory.>"},
	}
}

func newOTelConfig() platformotel.Config {
	return platformotel.Config{
		Endpoint:       envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		ServiceName:    "onlinemenu-api-pos",
		ServiceVersion: envOr("APP_VERSION", "dev"),
	}
}

func newVaultConfig() vault.Config {
	return vault.Config{
		Address: mustEnv("VAULT_ADDR"),
		Token:   mustEnv("VAULT_TOKEN"),
	}
}

func newCacheConfig() cache.Config {
	return cache.Config{
		Addr:     envOr("REDIS_ADDR", "localhost:6379"),
		Password: envOr("REDIS_PASSWORD", ""),
		DB:       0,
		PoolSize: 20,
	}
}

func newOPAConfig() auth.EngineConfig {
	return auth.EngineConfig{
		BundlePath: envOr("OPA_BUNDLE_PATH", "configs/opa/bundles"),
	}
}

func newHTTPConfig() httpConfig {
	return httpConfig{
		Addr:         envOr("HTTP_ADDR", ":8082"),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "api-pos: required env var %q is not set\n", key)
		os.Exit(1)
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Adım 3: Derlemeyi doğrula**

```bash
cd backend && go build ./cmd/api-pos/... && cd ..
```

Expected: hata yok

- [ ] **Adım 4: Commit**

```bash
git add backend/cmd/api-pos/
git commit -m "feat: api-pos binary — POS deployment grubu iskeleti"
```

---

### Task 10: `cmd/api-finance` binary iskeletini oluştur

**Files:**
- Create: `backend/cmd/api-finance/main.go`

Bu binary payment, billing, notification modüllerini yükleyecek. Faz 1'de implement edildiğinde buraya eklenir.

- [ ] **Adım 1: Dizin oluştur**

```bash
mkdir -p backend/cmd/api-finance
```

- [ ] **Adım 2: `main.go` oluştur**

`backend/cmd/api-finance/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/cache"
	"onlinemenu.tr/internal/platform/db"
	"onlinemenu.tr/internal/platform/eventbus"
	platformotel "onlinemenu.tr/internal/platform/otel"
	"onlinemenu.tr/internal/platform/vault"
)

// api-finance serves the finance deployment group: payment, billing, notification.
// Faz 1: payment.Module, billing.Module, notification.Module are wired here when implemented.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app := fx.New(
		fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
			return &fxevent.ZapLogger{Logger: log}
		}),

		fx.Provide(newLogger),
		fx.Provide(newDBConfig),
		fx.Provide(newEventBusConfig),
		fx.Provide(newOTelConfig),
		fx.Provide(newVaultConfig),
		fx.Provide(newCacheConfig),
		fx.Provide(newOPAConfig),
		fx.Provide(newHTTPConfig),

		db.Module,
		eventbus.Module,
		auth.Module,
		platformotel.Module,
		vault.Module,
		cache.Module,

		// payment.Module,      (Faz 1)
		// billing.Module,      (Faz 1)
		// notification.Module, (Faz 1)

		fx.Provide(newRouter),
		fx.Invoke(startHTTP),
	)

	app.Run()

	<-ctx.Done()
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := app.Stop(stopCtx); err != nil {
		fmt.Fprintf(os.Stderr, "api-finance: graceful shutdown error: %v\n", err)
		os.Exit(1)
	}
}

type httpConfig struct {
	Addr         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

func newRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, "api-finance")
	})
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return r
}

func startHTTP(lc fx.Lifecycle, cfg httpConfig, r *chi.Mux, log *zap.Logger) {
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			log.Info("api-finance HTTP server starting", zap.String("addr", cfg.Addr))
			go func() {
				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Error("api-finance HTTP server error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	})
}

func newLogger() (*zap.Logger, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, fmt.Errorf("api-finance: build logger: %w", err)
	}
	return logger, nil
}

func newDBConfig() db.Config {
	return db.Config{
		DSN:             mustEnv("DATABASE_URL"),
		MaxConns:        10,
		MinConns:        2,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}
}

func newEventBusConfig() eventbus.Config {
	return eventbus.Config{
		URL:        mustEnv("NATS_URL"),
		StreamName: "DOMAIN_EVENTS",
		Subjects:   []string{"payment.>", "billing.>", "notification.>"},
	}
}

func newOTelConfig() platformotel.Config {
	return platformotel.Config{
		Endpoint:       envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		ServiceName:    "onlinemenu-api-finance",
		ServiceVersion: envOr("APP_VERSION", "dev"),
	}
}

func newVaultConfig() vault.Config {
	return vault.Config{
		Address: mustEnv("VAULT_ADDR"),
		Token:   mustEnv("VAULT_TOKEN"),
	}
}

func newCacheConfig() cache.Config {
	return cache.Config{
		Addr:     envOr("REDIS_ADDR", "localhost:6379"),
		Password: envOr("REDIS_PASSWORD", ""),
		DB:       0,
		PoolSize: 10,
	}
}

func newOPAConfig() auth.EngineConfig {
	return auth.EngineConfig{
		BundlePath: envOr("OPA_BUNDLE_PATH", "configs/opa/bundles"),
	}
}

func newHTTPConfig() httpConfig {
	return httpConfig{
		Addr:         envOr("HTTP_ADDR", ":8083"),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "api-finance: required env var %q is not set\n", key)
		os.Exit(1)
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Adım 3: Derlemeyi doğrula**

```bash
cd backend && go build ./cmd/api-finance/... && cd ..
```

Expected: hata yok

- [ ] **Adım 4: Commit**

```bash
git add backend/cmd/api-finance/
git commit -m "feat: api-finance binary — finance deployment grubu iskeleti"
```

---

### Task 11: `go-arch-lint` kurallarını güncelle — yeni modüller

**Files:**
- Modify: `backend/.go-arch-lint.yml`

- [ ] **Adım 1: `backend/.go-arch-lint.yml`'e yeni modüller ekle**

`backend/.go-arch-lint.yml` dosyasını aç ve şununla değiştir:

```yaml
version: 2
workdir: .

components:
  platform:      { in: "internal/platform/**" }
  identity:      { in: "internal/modules/identity/**" }
  tenant:        { in: "internal/modules/tenant/**" }
  catalog:       { in: "internal/modules/catalog/**" }
  pos:           { in: "internal/modules/pos/**" }
  inventory:     { in: "internal/modules/inventory/**" }
  billing:       { in: "internal/modules/billing/**" }
  payment:       { in: "internal/modules/payment/**" }
  edge_sync:     { in: "internal/modules/edge_sync/**" }
  hr:            { in: "internal/modules/hr/**" }
  party:         { in: "internal/modules/party/**" }
  manufacturing: { in: "internal/modules/manufacturing/**" }
  notification:  { in: "internal/modules/notification/**" }

deps:
  identity:
    may_depend_on: [platform]
  tenant:
    may_depend_on: [platform]
  catalog:
    may_depend_on: [platform]
  hr:
    may_depend_on: [platform]
  party:
    may_depend_on: [platform]
  notification:
    may_depend_on: [platform]
  manufacturing:
    may_depend_on: [platform, inventory.public]
    must_not_depend_on: [pos.domain, pos.repo, billing.domain, payment.domain]
  inventory:
    may_depend_on: [platform, catalog.public]
    must_not_depend_on: [pos.domain, payment.domain, pos.repo, payment.repo]
  pos:
    may_depend_on: [platform, catalog.public, inventory.public, payment.public]
    must_not_depend_on: [inventory.repo, inventory.domain, billing.domain, catalog.domain, payment.repo]
  payment:
    may_depend_on: [platform, notification.public]
    must_not_depend_on: [pos.domain, pos.repo, billing.domain]
  billing:
    may_depend_on: [platform, notification.public]
    must_not_depend_on: [pos.domain, payment.domain, pos.repo, payment.repo]
  edge_sync:
    may_depend_on: [platform, catalog.public, pos.public, inventory.public]
    must_not_depend_on: [pos.repo, pos.domain, catalog.domain, inventory.domain]
```

- [ ] **Adım 2: Lint'i çalıştır**

```bash
cd backend && go-arch-lint check && cd ..
```

Expected: hata yok

- [ ] **Adım 3: Commit**

```bash
git add backend/.go-arch-lint.yml
git commit -m "chore: go-arch-lint'e manufacturing, notification, hr, party modülleri ekle"
```

---

### Task 12: `backend/Taskfile.yml` ve CI build'i yeni binary'lerle güncelle

**Files:**
- Modify: `backend/Taskfile.yml` — `build:local`'a yeni binary'ler ekle
- Modify: `.github/workflows/ci.yml` — `build` job'una yeni binary'ler ekle

- [ ] **Adım 1: `backend/Taskfile.yml` `build:local` görevini güncelle**

`backend/Taskfile.yml` içindeki `build:local` görevini şununla değiştir:

```yaml
  build:local:
    desc: "Lokal Go build (ko olmadan)"
    cmds:
      - go build -o bin/api ./cmd/api
      - go build -o bin/api-core ./cmd/api-core
      - go build -o bin/api-pos ./cmd/api-pos
      - go build -o bin/api-finance ./cmd/api-finance
      - go build -o bin/edge ./cmd/edge
      - go build -o bin/worker ./cmd/worker
```

- [ ] **Adım 2: CI `build` job'una yeni binary'leri ekle**

`.github/workflows/ci.yml` `build` job'undaki adımlara ekle:

```yaml
      - name: Build api-core
        working-directory: backend
        run: go build -o /dev/null ./cmd/api-core

      - name: Build api-pos
        working-directory: backend
        run: go build -o /dev/null ./cmd/api-pos

      - name: Build api-finance
        working-directory: backend
        run: go build -o /dev/null ./cmd/api-finance
```

- [ ] **Adım 3: `task backend:build:local` ile tüm binary'leri doğrula**

```bash
task backend:build:local
```

Expected: `bin/api`, `bin/api-core`, `bin/api-pos`, `bin/api-finance`, `bin/edge`, `bin/worker` derlenir

- [ ] **Adım 4: Commit**

```bash
git add backend/Taskfile.yml .github/workflows/ci.yml
git commit -m "chore: build görevlerine api-core, api-pos, api-finance binary'leri ekle"
```

---

### Task 13: Son doğrulama — tüm sistem

- [ ] **Adım 1: Tüm binary'lerin derlendiğini doğrula**

```bash
cd backend && go build ./... && cd ..
```

Expected: hata yok

- [ ] **Adım 2: Testlerin geçtiğini doğrula**

```bash
task backend:test
```

Expected: `ok` veya `no test files` — başarısız test yok

- [ ] **Adım 3: Lint'in geçtiğini doğrula**

```bash
task backend:lint
```

Expected: hata yok

- [ ] **Adım 4: Repo kök yapısını doğrula**

```bash
ls -la
```

Expected: `backend/`, `web/`, `mobile/`, `deploy/`, `docs/`, `Taskfile.yml` görünür; Go dosyaları (cmd/, internal/, go.mod) kök dizinde yok

- [ ] **Adım 5: `go.mod` module adının değişmediğini son kez doğrula**

```bash
head -1 backend/go.mod
```

Expected: `module onlinemenu.tr`

- [ ] **Adım 6: Final commit**

```bash
git add -A
git status
# Dirty dosya yoksa:
git log --oneline -10
```

Expected: Tüm commit'ler temiz; working tree clean
