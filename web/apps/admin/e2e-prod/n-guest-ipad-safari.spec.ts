// (n) Misafir QR akışı — iPad SAFARİ (webkit, yatay), Serdivan şubesi.
//
// l-guest-tablet.spec.ts aynı akışı Galaxy Tab S4 chromium emülasyonuyla
// kanıtlar; bu spec onun webkit karşılığıdır. Restoran masasındaki gerçek
// tablet çoğunlukla iPad'dir ve iPad'in tek tarayıcı motoru WebKit'tir;
// chromium emülasyonu webkit'e özgü farkları (fixed footer/scroll davranışı,
// dialog render'ı, çerezli 303 yönlendirme, Intl biçimlendirme) yakalayamaz.
// Akış l ile birebir: QR okut → yatay taşma kontrolü → iki farklı ürün →
// adet artır/kaldır → 47.000'lik tenant fiyatı → sipariş + sipariş
// listesi/detayı. Şube Serdivan'dır (m-guest-iphone-safari İzmit'i kullanır —
// aynı koşuda masa/adisyon çakışmasın diye şubeler ayrıldı).
//
// Misafir oturumu HttpOnly `om_guest` çerezinde, sepet localStorage'da yaşar
// (lib/cart-store.ts). Her test kendi context'ini alsaydı ikisi de sıfırlanırdı;
// serial testler bu yüzden beforeAll'da açılan TEK paylaşımlı context/page
// üzerinde koşar. test.use yalnız varsayılan fixture'ları etkiler — aynı cihaz
// tanımlayıcısı paylaşılan context'e newContext ile ayrıca verilir.

import * as fs from "node:fs"
import * as path from "node:path"

import {
  type APIRequestContext,
  type BrowserContext,
  type Page,
  devices,
  expect,
  test,
} from "@playwright/test"

import {
  ACCOUNTS,
  API_URL,
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
  withRateLimitRetry,
} from "./fixtures/prod"

const SMASH = "American Smash Burger"
const SERDIVAN_SMASH_PRICE = 47_000

// Misafir uçlarının ön eki (storefront/http/public_handler.go PublicAPIPrefix).
const PUBLIC_PREFIX = "/api/public/v1"

// Cihaz tanımlayıcısında browserName alanı yoktur; spread hem test.use'a hem
// newContext'e güvenle verilir (newContext browserName kabul etmez).
const IPAD = devices["iPad (gen 7) landscape"]

// Ekran görüntüleri repo içinde kalır; koşu makinesinde klasör yoksa açılır.
const ARTIFACTS = path.join(__dirname, "artifacts")

// formatKurus (web/apps/menu/src/lib/money.ts) ile birebir aynı biçim: UI'daki
// tutar metnine tam eşleşme için aynı Intl ayarları kullanılır.
const TRY_FORMATTER = new Intl.NumberFormat("tr-TR", {
  style: "currency",
  currency: "TRY",
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})
const fmt = (kurus: number) => TRY_FORMATTER.format(kurus / 100)

const escapeRe = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")

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

interface GuestModifierGroup {
  id: string
  name: string
  selection_type: "single" | "multiple"
  min_select: number
  max_select: number
  modifiers: { id: string; name: string; price_delta: number }[]
}

interface GuestProduct {
  id: string
  name: string
  price_amount: number
  is_available: boolean
  modifier_groups: GuestModifierGroup[]
}

interface GuestMenu {
  categories: { products: GuestProduct[] }[]
}

// l-guest-tablet.spec.ts'teki desenin kopyası — ortak fixtures dosyasına
// bilerek dokunulmuyor (eşzamanlı işlerle çakışmasın).
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

// ProductSheet'in ön seçimini (lib/modifiers.ts defaultSelection) yeniden
// üretir: zorunlu "single" gruplarda ilk seçenek seçili gelir, fiyat farkı
// birim fiyata eklenir. Beklenen sepet tutarı buradan türetilir.
function defaultDelta(product: GuestProduct): number {
  return product.modifier_groups.reduce((sum, group) => {
    const preselected =
      group.selection_type === "single" && group.min_select > 0 && group.modifiers.length > 0
    return preselected ? sum + group.modifiers[0].price_delta : sum
  }, 0)
}

// Zorunlu "multiple" grup ön seçimsizdir; ürünü iki dokunuşla sepete eklemek
// mümkün olmaz. İkinci ürün seçilirken bu ürünler elenir.
function requiresManualChoice(product: GuestProduct): boolean {
  return product.modifier_groups.some((g) => g.min_select > 0 && g.selection_type === "multiple")
}

// nginx anlık yükte 429/503 döndürebilir (deploy/nginx, OPS-003). Menü uygulaması
// bunu /q/error sayfasına (rate_limited | unavailable) çevirir. Bir kez daha
// denenir; başka her hata (örn. invalid_qr) gerçek bir bulgu olarak kalır.
async function gotoMenuViaQR(page: Page, token: string): Promise<void> {
  const response = await page.goto(`${MENU_URL}/q/${token}`)
  const landed = new URL(page.url())
  const throttledPage =
    landed.pathname === "/q/error" &&
    ["rate_limited", "unavailable"].includes(landed.searchParams.get("code") ?? "")
  const throttledStatus = response !== null && [429, 503].includes(response.status())
  if (throttledPage || throttledStatus) {
    await page.waitForTimeout(3_000)
    await page.goto(`${MENU_URL}/q/${token}`)
  }
  await expect(page).toHaveURL(/\/menu$/)
}

// Sayfa içi veri isteği rate limit'e takılırsa ekran "Tekrar dene" düğmesiyle
// hata durumuna düşer; bir kez tıklanıp toparlanır. Assertion'lar gevşetilmez —
// düğme yoksa hiçbir şey yapılmaz, içerik yine de görünmek zorundadır.
async function retryIfThrottled(page: Page): Promise<void> {
  const retry = page.getByRole("button", { name: "Tekrar dene" })
  if (await retry.isVisible().catch(() => false)) await retry.click()
}

// Menü satırına dokun → alt sayfa (ProductSheet) → "Sepete ekle". Zorunlu
// "single" gruplar ön seçili geldiği için iki dokunuş yeterlidir.
async function addToCart(page: Page, product: GuestProduct): Promise<void> {
  await page
    .getByRole("button", { name: new RegExp(`^${escapeRe(product.name)}`) })
    .first()
    .click()
  const sheet = page.getByRole("dialog", { name: product.name })
  await expect(sheet).toBeVisible()
  await sheet.getByRole("button", { name: /^Sepete ekle/ }).click()
  await expect(sheet).toBeHidden()
}

test.describe.configure({ mode: "serial" })

// browserName AÇIKÇA verilir: cihaz tanımındaki defaultBrowserType tek başına
// browser'ı seçmez, yalnız bilgilendiricidir.
test.use({ ...IPAD, browserName: "webkit" })

test.describe("(n) misafir QR akışı — iPad Safari yatay (Serdivan)", () => {
  let shared: APIRequestContext
  let manager: Principal
  let cashier: Principal
  let kitchen: Principal
  let branchId: string
  let tableId: string
  let token: string
  let context: BrowserContext
  let page: Page
  let smash: GuestProduct
  let second: GuestProduct
  let createdMenuId: string | null = null
  const menuName = `${TAG} Menü`

  // ProductSheet ön seçimleriyle birlikte smash'in birim fiyatı; test 2'de
  // adet 2'ye çıkarılıp diğer kalem silindikten sonra beklenen sepet toplamı.
  const expectedTotal = () => 2 * (smash.price_amount + defaultDelta(smash))

  test.beforeAll(async ({ playwright, browser }) => {
    shared = await playwright.request.newContext()
    manager = await principal(shared, ACCOUNTS.manager())
    cashier = await principal(shared, ACCOUNTS.cashierSerdivan())
    kitchen = await principal(shared, ACCOUNTS.kitchenSerdivan())
    branchId = cashier.ctx.branch_id as string
    expect(branchId, "Serdivan kasiyeri şube kapsamlı olmalı (SEC-005)").toBeTruthy()

    // Aktif menü guard'ı (e-guest-qr.spec.ts ile aynı gerekçe): misafir menüsü
    // menu_items read model'inden beslenir; aktif menü yoksa QR menüsü boş
    // döner. Prod'da kalıcı "Ana Menü" var; yoksa geçici PRODTEST menüsü
    // kurulur ve afterAll'da pasifleştirilir. Bu spec İKİ farklı ürün
    // gerektirdiği için geçici menüye smash'e ek bir ürün daha bağlanır.
    const menus = await json<Menu[]>(await manager.api.get("/api/v1/catalog/menus"), 200)
    if (!menus.some((m) => m.is_active)) {
      const catalog = await json<{ id: string; name: string }[]>(
        await manager.api.get("/api/v1/catalog/products"),
        200,
      )
      const smashProduct = catalog.find((p) => p.name === SMASH)
      expect(smashProduct, `katalogda ${SMASH} olmalı`).toBeTruthy()
      const secondProduct = catalog.find((p) => p.name !== SMASH)
      expect(secondProduct, "katalogda ikinci bir ürün olmalı").toBeTruthy()

      const existing = menus.find((m) => m.name === menuName)
      const body = {
        name: menuName,
        description: "Prod kabul testi — koşu sonunda pasifleştirilir",
        is_active: true,
        sort_order: 0,
      }
      if (existing) {
        await json<Menu>(await manager.api.put(`/api/v1/catalog/menus/${existing.id}`, body), 200)
        createdMenuId = existing.id
      } else {
        const menu = await json<Menu>(await manager.api.post("/api/v1/catalog/menus", body), 201)
        createdMenuId = menu.id
      }
      // Aynı ürün ikinci kez eklenemez (menu_items benzersiz); hata yutulur,
      // kalem menüde zaten vardır.
      for (const p of [smashProduct, secondProduct]) {
        await manager.api
          .post(`/api/v1/catalog/menus/${createdMenuId}/items`, {
            product_id: (p as { id: string }).id,
            is_active: true,
            sort_order: 0,
          })
          .catch(() => undefined)
      }
    }

    const table = (await allTables(manager.api, branchId)).find((t) => t.status === "empty")
    expect(table, "Serdivan'da boş masa olmalı").toBeTruthy()
    tableId = (table as { id: string }).id
    token = await freshToken(manager.api, branchId, tableId)

    // Ürün adları ve fiyatları tarayıcı akışından önce misafir API'siyle
    // öğrenilir: locator'lar gerçek isimlerle kurulur, tutar assertion'ları
    // canlı veriye sabitlenmeden 47.000'lik tenant fiyatına bağlanabilir.
    // Token rotate edilene dek geçerli kalır; aynı QR'ı sonra tarayıcı da
    // okur. Misafir oturumu çerezle taşındığı için kendi istek bağlamı gerekir.
    const guestCtx = await playwright.request.newContext({ baseURL: API_URL })
    try {
      const session = await json<{ branch_id: string }>(
        await withRateLimitRetry(() => guestCtx.post(`${PUBLIC_PREFIX}/sessions`, { data: { token } })),
        200,
      )
      expect(session.branch_id).toBe(branchId)
      const menu = await json<GuestMenu>(
        await withRateLimitRetry(() => guestCtx.get(`${PUBLIC_PREFIX}/menu`)),
        200,
      )
      const available = menu.categories.flatMap((c) => c.products).filter((p) => p.is_available)
      const foundSmash = available.find((p) => p.name === SMASH)
      expect(foundSmash, `misafir menüsünde ${SMASH} yok — aktif menu/menu_items kaydı gerekli`).toBeTruthy()
      smash = foundSmash as GuestProduct
      // Ad çakışması sepet satırı locator'larını (hasText) bulanıklaştırır;
      // ikinci ürün smash ile ad kesişmeyen, iki dokunuşla eklenebilen bir
      // üründür. Seçeneksiz ürün varsa o tercih edilir.
      const candidates = available.filter(
        (p) =>
          p.id !== smash.id &&
          !p.name.includes(SMASH) &&
          !SMASH.includes(p.name) &&
          !requiresManualChoice(p),
      )
      const foundSecond = candidates.find((p) => p.modifier_groups.length === 0) ?? candidates[0]
      expect(foundSecond, "misafir menüsünde ikinci bir ürün olmalı").toBeTruthy()
      second = foundSecond as GuestProduct
    } finally {
      await guestCtx.dispose()
    }

    fs.mkdirSync(ARTIFACTS, { recursive: true })
    // browser fixture'ı test.use browserName sayesinde webkit'tir; buradaki
    // spread yalnız cihaz profilini (viewport/UA/touch) taşır.
    context = await browser.newContext({ ...IPAD, locale: "tr-TR" })
    page = await context.newPage()
  })

  test.afterAll(async () => {
    // Teardown hataları yutulur — koşuyu düşüren şey test gövdesindeki
    // assertion'lardır; burası yalnız paylaşılan pilot tenant'ı temizler.
    await context?.close().catch(() => undefined)
    try {
      // Misafir siparişi adisyonu misafir oturumuna açar; UI akışında check_id
      // elde olmadığından masaya bağlı açık adisyon listeden bulunur
      // (pos/http/handler.go:82 GET /checks, status + branch_id filtreleri).
      const checks = await json<Check[]>(
        await cashier.api.get(`/api/v1/pos/checks?branch_id=${branchId}&status=open`),
        200,
      )
      const open = checks.find((c) => c.table_id === tableId)
      if (open) await cleanupCheck(cashier.api, kitchen.api, open.id)
    } catch {
      // yutulur — temizlik gerçek hatayı maskelemez
    }
    try {
      await releaseTable(manager.api, branchId, tableId)
    } catch {
      // yutulur
    }
    if (createdMenuId) {
      // Silme ucu yok; menü pasifleştirilir (güncelleme ucu PUT'tur).
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

  test("QR okutma ve iPad yatay yerleşimi", async () => {
    await gotoMenuViaQR(page, token)
    await retryIfThrottled(page)

    await expect(page.getByText(SMASH).first()).toBeVisible()

    // Yatay iPad genişliğinde sayfa yatay taşma üretmemeli: max-w-screen-sm
    // gövde ortalanır, fixed sepet çubuğu viewport'a sığar. 1 px tolerans
    // deviceScaleFactor yuvarlamaları içindir, gerçek taşmayı örtmez.
    const overflow = await page.evaluate(() => {
      const el = document.scrollingElement
      return el === null ? 0 : el.scrollWidth - el.clientWidth
    })
    expect(overflow, "iPad yatay görünümde yatay scroll oluşmamalı").toBeLessThanOrEqual(1)

    await page.screenshot({ path: path.join(ARTIFACTS, "safari-ipad-01-menu.png"), fullPage: true })
  })

  test("çok kalemli sepet aksiyonları", async () => {
    expect(smash.price_amount, "Serdivan QR menüsü tenant fiyatını göstermeli").toBe(
      SERDIVAN_SMASH_PRICE,
    )

    await addToCart(page, smash)
    await addToCart(page, second)

    // Sepet çubuğu toplam adedi gösterir; iki farklı kalem = 2 ürün.
    const cartLink = page.getByRole("link", { name: /2 ürün/ })
    await expect(cartLink).toBeVisible()
    await cartLink.click()
    await expect(page).toHaveURL(/\/cart$/)

    const smashRow = page.locator("li").filter({ hasText: smash.name })
    const otherRow = page.locator("li").filter({ hasText: second.name })
    await expect(smashRow).toHaveCount(1)
    await expect(otherRow).toHaveCount(1)

    await smashRow.getByRole("button", { name: "Bir adet artır" }).click()
    await expect(smashRow.getByText("2", { exact: true })).toBeVisible()

    await otherRow.getByRole("button", { name: "Kaldır" }).click()
    await expect(otherRow).toHaveCount(0)

    // Kalan tek kalem: 2 × (47.000 + ön seçili zorunlu seçenek farkları).
    // Tutar, UI ile aynı Intl biçimlendirmesiyle tam metin eşleşmesidir.
    const footer = page.locator("div.fixed")
    await expect(footer.getByText("Toplam")).toBeVisible()
    await expect(footer.getByText(fmt(expectedTotal()), { exact: true })).toBeVisible()

    await page.screenshot({ path: path.join(ARTIFACTS, "safari-ipad-02-cart-actions.png"), fullPage: true })
  })

  test("sipariş ver + sipariş listesi", async () => {
    await expect(page).toHaveURL(/\/cart$/)
    await page.getByRole("button", { name: "Siparişi gönder" }).click()

    // 429/5xx'te ekran hatayı gösterir ve Idempotency-Key'i saklar
    // (cart-store beginSubmission): bir kez daha gönderme çift sipariş
    // yaratmaz, sunucu ilk yanıtı yeniden oynatır.
    const placed = await page
      .waitForURL(/\/orders\/[^/?]+/, { timeout: 30_000 })
      .then(() => true)
      .catch(() => false)
    if (!placed) {
      await page.getByRole("button", { name: "Siparişi gönder" }).click()
      await page.waitForURL(/\/orders\/[^/?]+/, { timeout: 30_000 })
    }

    // Gönderim sonrası doğrudan sipariş detayına düşülür (?placed=1 banner'ı).
    await expect(page.getByText("Siparişiniz alındı")).toBeVisible()
    await expect(page.getByText("Onay bekliyor").first()).toBeVisible()

    // Sipariş listesi: başlıktaki "Siparişlerim" bağlantısı SPA gezinmesidir,
    // misafir çerezi ve sepet oturumu korunur.
    await page.getByRole("link", { name: "Siparişlerim" }).click()
    await expect(page).toHaveURL(/\/orders$/)
    await retryIfThrottled(page)
    await expect(page.getByRole("heading", { name: "Siparişlerim" })).toBeVisible()

    // Liste kaydının erişilebilir adı durum rozetini içerir.
    const entry = page.getByRole("link", { name: /Onay bekliyor/ }).first()
    await expect(entry).toBeVisible()
    await entry.click()
    await page.waitForURL(/\/orders\/[^/?]+$/)

    await expect(page.getByRole("heading", { name: "Sipariş durumu" })).toBeVisible()
    await expect(page.getByRole("heading", { name: "Ürünler" })).toBeVisible()
    await expect(page.getByText(smash.name).first()).toBeVisible()
    // Sunucu fiyatı menü read model'inden yeniden türetir; toplam, sepetle
    // aynı tutarı göstermek zorundadır (2 × Serdivan birim fiyatı).
    await expect(page.getByText(fmt(expectedTotal())).first()).toBeVisible()

    await page.screenshot({ path: path.join(ARTIFACTS, "safari-ipad-03-orders.png"), fullPage: true })
  })
})
