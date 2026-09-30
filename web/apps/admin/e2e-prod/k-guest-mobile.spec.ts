// (k) Misafir QR sipariş akışı — MOBİL tarayıcı görünümü (ADR-ARCH-006).
// (e) spec'i misafir API sözleşmesini kanıtlar; bu spec aynı akışın gerçek
// arayüzünü kanıtlar: QR okut → menü → ürün → sepet → sipariş → durum.
// `${MENU_URL}/q/${token}` adresine gitmek, telefonda QR taramanın birebir
// karşılığıdır (menu/src/app/q/[token]/route.ts oturum çerezini kurup
// /menu'ye 303 ile yönlendirir; hata durumunda /q/error).
//
// Aktif menü guard'ı (e) spec'inden aynen taşındı: prod'da aktif menü yoksa
// misafir menüsü boş döner (catalog storefront read model'i menu_items'tan
// beslenir), o yüzden geçici PRODTEST Menü kurulur ve koşu sonunda
// pasifleştirilir.

import fs from "node:fs"
import path from "node:path"

import { type APIRequestContext, type Page, devices, expect, test } from "@playwright/test"

import {
  ACCOUNTS,
  type Api,
  type Check,
  MENU_URL,
  type Principal,
  TAG,
  allTables,
  cleanupCheck,
  json,
  principal,
  releaseTable,
} from "./fixtures/prod"

// webkit bu makinede kurulu değil; Pixel 7 chromium tabanlı bir cihaz
// tanımıdır (mobil viewport + touch + mobil UA).
test.use({ ...devices["Pixel 7"] })

test.describe.configure({ mode: "serial" })

const SMASH = "American Smash Burger"
const IZMIT_SMASH_PRICE = 49_000

// menu uygulamasının lib/money.ts biçimlendirmesiyle birebir aynı: tutar
// kuruş cinsinden gelir, yalnız render anında 100'e bölünür.
const TRY_FORMATTER = new Intl.NumberFormat("tr-TR", {
  style: "currency",
  currency: "TRY",
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

function formatKurus(amount: number): string {
  return TRY_FORMATTER.format(amount / 100)
}

const ARTIFACTS_DIR = path.join(__dirname, "artifacts")

async function shot(page: Page, name: string): Promise<void> {
  await page.screenshot({ path: path.join(ARTIFACTS_DIR, `${name}.png`), fullPage: false })
}

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

// QR token'ı yalnız rotate yanıtında döner (DB'de sadece token_hash tutulur),
// bu yüzden her koşu taze token üretir; hiçbir token repoda saklanmaz.
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

test.describe("(k) misafir QR sipariş akışı — mobil", () => {
  let shared: APIRequestContext
  let manager: Principal
  let cashier: Principal
  let kitchen: Principal
  let page: Page
  let branchId: string
  let tableId: string
  let tableLabel: string
  let qrToken: string
  let createdMenuId: string | null = null
  const menuName = `${TAG} Menü`

  test.beforeAll(async ({ playwright }) => {
    fs.mkdirSync(ARTIFACTS_DIR, { recursive: true })

    shared = await playwright.request.newContext()
    manager = await principal(shared, ACCOUNTS.manager())
    cashier = await principal(shared, ACCOUNTS.cashierIzmit())
    kitchen = await principal(shared, ACCOUNTS.kitchenIzmit())
    branchId = cashier.ctx.branch_id as string

    const table = (await allTables(manager.api, branchId)).find((t) => t.status === "empty")
    expect(table, "İzmit şubesinde boş masa olmalı").toBeTruthy()
    tableId = (table as { id: string }).id
    tableLabel = (table as { name: string }).name
    qrToken = await freshToken(manager.api, branchId, tableId)

    // Aktif menü guard'ı — (e) spec'iyle aynı: silme ucu olmadığı için önceki
    // koşudan kalan pasif PRODTEST menüsü yeniden kullanılır.
    const menus = await json<Menu[]>(await manager.api.get("/api/v1/catalog/menus"), 200)
    if (menus.some((m) => m.is_active)) return

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

    // Aynı ürün menüye iki kez eklenemez (menu_items benzersiz); hata yutulur.
    await manager.api
      .post(`/api/v1/catalog/menus/${createdMenuId}/items`, {
        product_id: (smash as { id: string }).id,
        is_active: true,
        sort_order: 0,
      })
      .catch(() => undefined)
  })

  test.beforeAll(async ({ browser }) => {
    // Misafir oturumu HttpOnly çerezde, sepet localStorage'da yaşar; her test
    // için yeni page fixture'ı ikisini de sıfırlardı. Akış testler arasında
    // sürdüğü için sayfa bir kez burada, aynı mobil cihaz profiliyle açılır.
    const context = await browser.newContext({ ...devices["Pixel 7"], locale: "tr-TR" })
    page = await context.newPage()
  })

  test.afterAll(async () => {
    // Teardown asıl başarısızlığı maskelememeli — her adım kendi başına
    // yutulur; paylaşılan pilot tenant'ı sonraki koşu için toparlar.
    try {
      // Misafir akışında check_id istemcinin elinde yoktur; masaya bağlı açık
      // adisyon şube listesinden bulunur (GET /api/v1/pos/checks, status=open).
      const open = await json<Check[]>(
        await cashier.api.get(`/api/v1/pos/checks?branch_id=${branchId}&status=open`),
        200,
      )
      const mine = open.find((c) => c.table_id === tableId)
      if (mine) await cleanupCheck(cashier.api, kitchen.api, mine.id)
      await releaseTable(manager.api, branchId, tableId)
    } catch {
      // ignored — teardown
    }
    try {
      if (createdMenuId) {
        // Silme ucu yok; menü pasifleştirilir. Güncelleme ucu PUT'tur.
        await manager.api.put(`/api/v1/catalog/menus/${createdMenuId}`, {
          name: menuName,
          description: "Prod kabul testi — pasif",
          is_active: false,
          sort_order: 0,
        })
      }
    } catch {
      // ignored — teardown
    }
    await page?.context().close().catch(() => undefined)
    await shared?.dispose()
  })

  test("QR okutma: /q/{token} menüye düşer, masa etiketi ve ürün görünür", async () => {
    await page.goto(`${MENU_URL}/q/${qrToken}`)

    // nginx hız sınırı (429/503) /q yönlendirmesini /q/error'a düşürebilir;
    // gerçek bir misafir QR'ı yeniden okutur — bir kez, bekleyip tekrar
    // denenir, ikinci başarısızlık gerçek hata olarak kalır.
    if (new URL(page.url()).pathname.startsWith("/q/error")) {
      await page.waitForTimeout(3_000)
      await page.goto(`${MENU_URL}/q/${qrToken}`)
    }
    await expect(page).toHaveURL(`${MENU_URL}/menu`)

    // Üst çubuk masa etiketini oturum çerezinin eşlik çerezinden okur
    // (app-shell.tsx + use-table-session.ts).
    await expect(page.getByRole("banner").getByText(tableLabel)).toBeVisible()

    // Menü içeriği tarayıcıdan ayrı bir API çağrısıyla gelir ve o da hız
    // sınırına takılabilir; hata durumunda ekrandaki "Tekrar dene" bir kez
    // kullanılır (errors.menuLoadFailed / common.retry — messages/tr.json).
    const smashRow = page.getByRole("button", { name: SMASH })
    const menuError = page.getByText("Menü yüklenemedi.")
    await expect(smashRow.or(menuError).first()).toBeVisible()
    if (await menuError.isVisible().catch(() => false)) {
      await page.waitForTimeout(3_000)
      await page.getByRole("button", { name: "Tekrar dene" }).click()
    }
    await expect(smashRow).toBeVisible()

    await shot(page, "mobile-01-menu")
  })

  test("ürün ekle + sepet: product sheet, cart bar ve İzmit şube fiyatı", async () => {
    await page.getByRole("button", { name: SMASH }).click()

    const sheet = page.getByRole("dialog")
    await expect(sheet).toBeVisible()
    // Düğme metni toplam fiyatı taşır (product.addWithPrice): fiyat İzmit
    // şube override'ından gelmelidir, tenant fiyatından değil (ADR-DATA-009).
    const addButton = sheet.getByRole("button", {
      name: `Sepete ekle · ${formatKurus(IZMIT_SMASH_PRICE)}`,
    })
    await expect(addButton).toBeVisible()
    await shot(page, "mobile-02-product-sheet")

    await addButton.click()
    await expect(sheet).not.toBeVisible()

    // Cart bar alt kenara sabit tek erişim yoludur (cart-bar.tsx); metni
    // menu.itemCount kalıbından gelir.
    const cartLink = page.getByRole("link", { name: "1 ürün" })
    await expect(cartLink).toBeVisible()
    await expect(cartLink).toContainText(formatKurus(IZMIT_SMASH_PRICE))

    await cartLink.click()
    await expect(page).toHaveURL(`${MENU_URL}/cart`)

    await page.getByRole("button", { name: "Bir adet artır" }).click()
    // 980 TL yalnız 2 × 490 TL'den (İzmit fiyatı) çıkabilir; kalem toplamı ve
    // sepet toplamı aynı metni gösterdiği için first() ile yetinilir.
    await expect(page.getByText("Toplam", { exact: true })).toBeVisible()
    await expect(page.getByText(formatKurus(2 * IZMIT_SMASH_PRICE)).first()).toBeVisible()

    await shot(page, "mobile-03-cart")
  })

  test("sipariş ver + durum: gönderim sipariş durumu ekranına düşer", async () => {
    const submit = page.getByRole("button", { name: "Siparişi gönder" })
    await expect(submit).toBeEnabled()
    await submit.click()

    try {
      await page.waitForURL((url) => url.pathname.startsWith("/orders/"), { timeout: 20_000 })
    } catch {
      // 429/503'te hata metni görünür ve düğme yeniden basılabilir olur;
      // idempotency anahtarı korunduğu için (cart-store.beginSubmission,
      // ADR-SEC-003) yeniden basmak çift sipariş yaratmaz.
      await expect(page.getByRole("alert")).toBeVisible()
      await page.waitForTimeout(3_000)
      await submit.click()
      await page.waitForURL((url) => url.pathname.startsWith("/orders/"), { timeout: 30_000 })
    }

    // ?placed=1 onay bandı + durum rozeti (order-detail.tsx; metinler
    // confirmation.title ve orders.status.pending).
    await expect(page.getByText("Siparişiniz alındı")).toBeVisible()
    await expect(page.getByRole("heading", { name: "Sipariş durumu" })).toBeVisible()
    await expect(page.getByText("Onay bekliyor")).toBeVisible()
    await expect(page.getByText(formatKurus(2 * IZMIT_SMASH_PRICE)).first()).toBeVisible()

    await shot(page, "mobile-04-order-status")
  })
})
