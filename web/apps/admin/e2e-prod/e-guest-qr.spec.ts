// (e) Misafir QR menüsü (ADR-ARCH-006): masanın QR'ı hangi şubeye aitse
// menü fiyatı o şubenin fiyatıdır.
//
// QR token'ı yalnız oluşturma/rotate yanıtında döner (DB'de sadece token_hash).
// Spec bu yüzden mevcut QR'ı rotate ederek taze token alır — hiçbir token
// repoda veya ortam değişkeninde tutulmaz.
//
// BULGU (2026-09-20): prod'da hiç `menus` kaydı yok ve misafir menüsü
// read model'i menu_items'tan beslenir
// (catalog/repo/storefront_menu_repo.go:104 `visible_items` CTE) — yani QR
// menüsü bugün BOŞ döner. docs/b2b-import-plan.md §5 "QR menüsü menüsüz
// çözülür" satırı hatalıdır. Bu spec, fiyat çözümünü kanıtlayabilmek için
// geçici bir `PRODTEST Menü` açar ve sonunda pasifleştirir; kalıcı menü
// kararı ürün tarafına aittir.

import { type APIRequestContext, expect, test } from "@playwright/test"

import {
  ACCOUNTS,
  API_URL,
  type Api,
  type Principal,
  RUN,
  TAG,
  allTables,
  json,
  principal,
  releaseTable,
  withRateLimitRetry,
} from "./fixtures/prod"

const SMASH = "American Smash Burger"

// Misafir uçları personel kimlik zincirinin dışındadır ve kendi ön eki vardır
// (storefront/http/public_handler.go: PublicAPIPrefix). menu.diverstreetfood.com
// yalnız Next.js uygulamasını sunar; API çağrıları api.diverstreetfood.com'a
// gider (web/apps/menu/src/lib/api.ts NEXT_PUBLIC_PUBLIC_API_URL).
const PUBLIC_PREFIX = "/api/public/v1"

interface QRCode {
  id: string
  table_id: string
  status: string
}

interface Menu {
  id: string
  name: string
  is_active: boolean
}

interface GuestMenu {
  categories: { products: { id: string; name: string; price_amount: number }[] }[]
}

async function freshToken(manager: Api, branchId: string, tableId: string): Promise<string> {
  const codes = await json<QRCode[]>(await manager.get(`/api/v1/storefront/qr-codes?branch_id=${branchId}`), 200)
  const active = codes.find((c) => c.table_id === tableId && c.status === "active")
  expect(active, `masa ${tableId} için aktif QR yok`).toBeTruthy()
  const rotated = await json<{ token: string }>(
    await manager.post(`/api/v1/storefront/qr-codes/${(active as QRCode).id}/rotate`),
    200,
  )
  return rotated.token
}

test.describe.configure({ mode: "serial" })

test.describe("(e) misafir QR menüsü", () => {
  let manager: Principal
  let shared: APIRequestContext
  let createdMenuId: string | null = null
  const menuName = `${TAG} Menü`

  test.beforeAll(async ({ playwright }) => {
    shared = await playwright.request.newContext()
    manager = await principal(shared, ACCOUNTS.manager())

    const menus = await json<Menu[]>(await manager.api.get("/api/v1/catalog/menus"), 200)
    if (menus.some((m) => m.is_active)) return

    // Aktif menü yoksa geçici olanı kur — tenant geneli (branch_id yok), her
    // şubede görünür, fiyat şube override'ıyla çözülür. Silme ucu olmadığı
    // için önceki koşudan kalan pasif PRODTEST menüsü yeniden kullanılır;
    // aksi hâlde her koşu kataloğa bir ölü satır daha bırakırdı.
    const existing = menus.find((m) => m.name === menuName)
    const products = await json<{ id: string; name: string }[]>(
      await manager.api.get("/api/v1/catalog/products"),
      200,
    )
    const smash = products.find((p) => p.name === SMASH)
    expect(smash, `katalogda ${SMASH} olmalı`).toBeTruthy()

    if (existing) {
      await json<Menu>(
        await manager.api.put(`/api/v1/catalog/menus/${existing.id}`, {
          name: menuName,
          description: "Prod kabul testi — koşu sonunda pasifleştirilir",
          is_active: true,
          sort_order: 0,
        }),
        200,
      )
      createdMenuId = existing.id
    } else {
      const menu = await json<Menu>(
        await manager.api.post("/api/v1/catalog/menus", {
          name: menuName,
          description: "Prod kabul testi — koşu sonunda pasifleştirilir",
          is_active: true,
          sort_order: 0,
        }),
        201,
      )
      createdMenuId = menu.id
    }

    // Kalem ekleme idempotent değilse de aynı ürün iki kez eklenemez
    // (menu_items benzersiz); hata yutulur, menüde zaten vardır.
    await manager.api
      .post(`/api/v1/catalog/menus/${createdMenuId}/items`, {
        product_id: (smash as { id: string }).id,
        is_active: true,
        sort_order: 0,
      })
      .catch(() => undefined)
  })

  test.afterAll(async () => {
    if (createdMenuId) {
      // Silme ucu yok; menü pasifleştirilir (QR menüsü yeniden boşalır).
      // Güncelleme ucu PUT'tur (catalog/http/handler.go:110), PATCH değil.
      await manager.api
        .put(`/api/v1/catalog/menus/${createdMenuId}`, {
          name: menuName,
          description: "Prod kabul testi — pasif",
          is_active: false,
          sort_order: 0,
        })
        .catch(() => undefined)
    }
    await shared.dispose()
  })

  test("İzmit QR'ı 490 TL, Serdivan QR'ı 470 TL gösterir", async ({ playwright }) => {
    const izmit = await principal(shared, ACCOUNTS.cashierIzmit())
    const serdivan = await principal(shared, ACCOUNTS.cashierSerdivan())

    const prices: Record<string, number> = {}
    for (const [name, branchId] of [
      ["izmit", izmit.ctx.branch_id as string],
      ["serdivan", serdivan.ctx.branch_id as string],
    ] as const) {
      const table = (await allTables(manager.api, branchId)).find((t) => t.status === "empty")
      expect(table, `${name} şubesinde boş masa olmalı`).toBeTruthy()
      const token = await freshToken(manager.api, branchId, (table as { id: string }).id)

      // Misafir oturumu bir çerezle taşınır — kendi istek bağlamı gerekir.
      const guest = await playwright.request.newContext({ baseURL: API_URL })
      try {
        const session = await json<{ branch_id: string; table_label: string }>(
          await withRateLimitRetry(() => guest.post(`${PUBLIC_PREFIX}/sessions`, { data: { token } })),
          200,
        )
        expect(session.branch_id).toBe(branchId)

        const menu = await json<GuestMenu>(await withRateLimitRetry(() => guest.get(`${PUBLIC_PREFIX}/menu`)), 200)
        const product = menu.categories.flatMap((c) => c.products).find((p) => p.name === SMASH)
        expect(
          product,
          `${name} misafir menüsünde ${SMASH} yok — aktif menu/menu_items kaydı olmadan QR menüsü boştur`,
        ).toBeTruthy()
        prices[name] = (product as { price_amount: number }).price_amount
      } finally {
        await guest.dispose()
      }
    }

    expect(prices.izmit, "İzmit QR menüsü şube fiyatını göstermeli").toBe(49_000)
    expect(prices.serdivan, "Serdivan QR menüsü tenant fiyatını göstermeli").toBe(47_000)
  })

  test("misafir sipariş verir; fiyatı sunucu belirler", async ({ playwright }) => {
    const izmit = await principal(shared, ACCOUNTS.cashierIzmit())
    const kitchen = await principal(shared, ACCOUNTS.kitchenIzmit())
    const branchId = izmit.ctx.branch_id as string
    const table = (await allTables(manager.api, branchId)).find((t) => t.status === "empty")
    expect(table).toBeTruthy()
    const tableId = (table as { id: string }).id
    const token = await freshToken(manager.api, branchId, tableId)

    const guest = await playwright.request.newContext({ baseURL: API_URL })
    let checkId: string | undefined
    try {
      await json(await withRateLimitRetry(() => guest.post(`${PUBLIC_PREFIX}/sessions`, { data: { token } })), 200)
      const menu = await json<GuestMenu>(await withRateLimitRetry(() => guest.get(`${PUBLIC_PREFIX}/menu`)), 200)
      const product = menu.categories.flatMap((c) => c.products).find((p) => p.name === SMASH)!

      const placed = await json<{ order_id: string; check_id: string; total: number; status: string }>(
        await withRateLimitRetry(() =>
          guest.post(`${PUBLIC_PREFIX}/orders`, {
            // Aynı anahtarla yeniden deneme çift sipariş yaratmaz
            // (httpx.IdempotencyWithScope, misafir oturumu kapsamlı).
            headers: { "Idempotency-Key": `${RUN}-guest` },
            data: { lines: [{ product_id: product.id, quantity: 1, modifier_ids: [] }], note: `${RUN} misafir` },
          }),
        ),
        201,
      )
      checkId = placed.check_id
      // Fiyat istemciden gelmez (placeOrderRequest'te fiyat alanı yok);
      // sunucu şube fiyatını uygular.
      expect(placed.total).toBe(49_000)

      const status = await json<{ status: string }>(await withRateLimitRetry(() => guest.get(`${PUBLIC_PREFIX}/orders/${placed.order_id}`)), 200)
      expect(status.status).toBeTruthy()
    } finally {
      await guest.dispose()
      if (checkId) {
        const { cleanupCheck } = await import("./fixtures/prod")
        await cleanupCheck(izmit.api, kitchen.api, checkId)
      }
      await releaseTable(manager.api, branchId, tableId)
    }
  })
})
