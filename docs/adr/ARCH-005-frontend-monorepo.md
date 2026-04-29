# ADR-ARCH-005: Frontend Monorepo Yapısı (pnpm workspaces)

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**Kategori:** Mimari (ARCH)

## Bağlam

Admin paneli (Next.js 16) ve POS desktop (Wails v2 + React) arasında UI komponent, TypeScript tipi ve konfigürasyon paylaşımı gerekli. İki uygulama aynı shadcn/ui tabanlı design system'i kullanacak; aynı event şemalarından türetilen tipleri paylaşacak.

Paylaşımsız yaklaşım: aynı `Button`, `Card`, `DataTable` bileşenleri iki ayrı yerde maintain edilir. Tek bir tema değişikliği iki ayrı PR gerektirir. Drift garantili.

## Karar

`web/` altında **pnpm workspaces** kullanılır.

```
web/
├── pnpm-workspace.yaml
├── package.json              (root devDependencies)
├── apps/
│   ├── admin/                (Next.js 16 App Router)
│   └── pos-desktop/          (Wails v2 + React frontend)
└── packages/
    ├── ui-kit/               (@onlinemenu/ui-kit — shadcn wrapper)
    ├── types/                (@onlinemenu/types — event şemalarından üretilir)
    └── config/               (@onlinemenu/config — eslint, tsconfig, tailwind)
```

**Kurallar:**
- `apps/admin` ve `apps/pos-desktop` **birbirinden import edemez**. Ortak kod `packages/*` altına çıkarılır.
- Bu kural `.eslintrc` `no-restricted-imports` ile zorlanır.
- `pnpm install --frozen-lockfile` CI'da zorunlu.
- Yeni bileşen önce `packages/ui-kit`'e eklenir; uygulama spesifik ise `apps/` altında kalır.

## Sonuçlar

**İyi:**
- Design token değişikliği tek PR ile iki uygulamaya yayılır.
- `packages/types` backend event sözleşmeleriyle senkron; `make gen:events` çıktısı buraya düşer.
- `packages/config` ile eslint/tsconfig tekrar edilmez.

**Kötü:**
- Workspace hoisting bazen beklenmedik bağımlılık çözümüne yol açar; `pnpm install` sonrası test gerekir.
- `apps` arası import eslint ile zorlanmazsa kolayca yapılabilir.

## Değerlendirilen Alternatifler

- **Nx / Turborepo:** Reddedildi (şimdilik). 2 uygulama + 3 paket için build cache overhead gereksiz. Faz 4'te (Flutter web / ek uygulama) Turborepo değerlendirilir.
- **npm/yarn workspaces:** Reddedildi. pnpm disk kullanımı ve install hızı avantajı net.
- **Tek dizin, copy-paste paylaşım:** Reddedildi. Drift garantili anti-pattern.
