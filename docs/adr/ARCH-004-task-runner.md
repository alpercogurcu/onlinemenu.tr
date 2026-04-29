# ADR-ARCH-004: Task Runner Seçimi (Taskfile)

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**Kategori:** Mimari (ARCH)

## Bağlam

Geliştirici: AI agent. Birden fazla AI seansı, farklı bağlamlarda aynı projeye dokunur. "Bu komut nasıl çalıştırılır?" sorusunun tek ve tutarlı bir cevabı olmalı.

Mevcut komutlar: `go run`, `npm run`, `docker compose`, `migrate`, `sqlc generate`, `ko build`, `golangci-lint`, `gosec`, `trivy`. Bunları her yerde farklı sözdizimi ile çağırmak:
- AI'ın farklı seanslarda tutarsız komut kullanmasına yol açar
- Windows uyumsuzluğu (Makefile + bash) ihtimali
- "Hangi komut nasıl çalıştırılır?" sorusu tekrar tekrar sorulur

## Karar

**Taskfile (go-task)** kullanılır. Makefile projeye girmez.

Konvansiyon: `<domain>:<action>` (ör: `migrate:up`, `test:integration`, `security:scan`).

AI her CLI komutu çalıştırmak istediğinde `task <name>` kullanır. Taskfile'da karşılığı yoksa önce görevi ekler. Mevcut görevler: `task --list`.

## Sonuçlar

**İyi:**
- Cross-platform (Windows/macOS/Linux tutarlı).
- YAML formatı AI için okunaklı; Makefile tab/shell syntax karmaşası yok.
- `deps:` ile bağımlılık grafiği açık ve kademeli çalıştırma mümkün.
- `task --list` ile keşif kolay.

**Kötü:**
- Taskfile içeriği şişebilir. 50+ task sonrası `tasks/*.yml` include yapısına geçiş gerekebilir.

**Risk:**
- `go-task` binary kurulu olmalı (CI'a `brew install go-task` veya binary download eklenir).

## Değerlendirilen Alternatifler

- **Make:** Reddedildi. Tab duyarlılığı AI-hostile; Windows uyumsuz; POSIX sh vs bash farkları.
- **npm scripts:** Reddedildi. Go-ağırlıklı projede tuhaf; `package.json` şişer.
- **just:** Değerlendirildi. Taskfile ile benzer ancak Go ecosystem entegrasyonu ve community daha zayıf.
- **Mage:** Reddedildi. Task'ları Go kodu olarak yazmak gereksiz overhead; AI için YAML daha okunaklı.
