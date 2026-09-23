// (h) Rol bazlı görünürlük — canlı test hesaplarıyla.
//
// Şikâyet (2026-09-23): garson düzenleme ekranlarını görüyor, kaydedince hata
// alıyordu. Menü ve ekran erişimi artık üretilmiş OPA matrisinden türüyor
// (lib/route-permissions.ts). Bu paket her rol için:
//   - girişte doğru ana sayfaya düşer,
//   - menüde yalnız beklenen bağlantılar vardır,
//   - yasak ekranlarda "erişim yok" görünür ve API'ye 4xx üreten istek gitmez.
// e2e/roles.spec.ts'in prod aynası. Her rol ayrı tarayıcı bağlamında açılır:
// Keycloak SSO çerezi paylaşılırsa ikinci rol ilkinin oturumuyla girer.

import { type Browser, type Page, expect, test } from "@playwright/test"

import { ACCOUNTS, API_URL, type Account, gotoSpa, loginAdmin } from "./fixtures/prod"

test.describe.configure({ mode: "serial" })

interface RoleCase {
  name: string
  account: () => Account
  home: string
  menu: string[] | null
  forbidden: string[]
}

const CASES: RoleCase[] = [
  {
    name: "garson (Serdivan)",
    account: ACCOUNTS.waiterSerdivan,
    home: "/pos/tables",
    menu: ["/pos/tables", "/pos/checks"],
    forbidden: [
      "/",
      "/catalog/products",
      "/catalog/products/new",
      "/catalog/categories",
      "/pos/kitchen",
      "/settings/users",
      "/payment/payments",
    ],
  },
  {
    name: "kasiyer (Serdivan)",
    account: ACCOUNTS.cashierSerdivan,
    home: "/pos/checks",
    menu: ["/pos/tables", "/pos/checks", "/pos/kitchen"],
    forbidden: ["/catalog/products", "/catalog/branch-pricing", "/settings/branches", "/payment/payments"],
  },
  {
    name: "mutfak (Serdivan)",
    account: ACCOUNTS.kitchenSerdivan,
    home: "/pos/kitchen",
    menu: ["/pos/tables", "/pos/kitchen"],
    forbidden: ["/pos/checks", "/catalog/products", "/settings/users"],
  },
  {
    // Manager's menu also depends on the tenant's enabled modules; only the
    // landing page and the absence of any denial are asserted.
    name: "yönetici",
    account: ACCOUNTS.manager,
    home: "/",
    menu: null,
    forbidden: [],
  },
]

async function sidebarLinks(page: Page): Promise<string[]> {
  return page
    .locator('a[data-sidebar="menu-button"][href]')
    .evaluateAll((els) => els.map((el) => el.getAttribute("href") ?? ""))
}

function watchApi4xx(page: Page): string[] {
  const errors: string[] = []
  page.on("response", (res) => {
    const url = res.url()
    const isApi = url.startsWith(API_URL) || url.includes("/api/core/")
    if (isApi && res.status() >= 400 && res.status() < 500) {
      errors.push(`${res.status()} ${res.request().method()} ${url}`)
    }
  })
  return errors
}

async function openAs(browser: Browser, acct: Account): Promise<{ page: Page; errors: string[] }> {
  const context = await browser.newContext()
  const page = await context.newPage()
  const errors = watchApi4xx(page)
  await loginAdmin(page, acct)
  return { page, errors }
}

test.describe("(h) rol bazlı görünürlük", () => {
  for (const c of CASES) {
    test(`${c.name}: ana sayfa, menü, yasak ekranlar`, async ({ browser }) => {
      const { page, errors } = await openAs(browser, c.account())
      try {
        await page.waitForURL((url) => url.pathname === c.home, { timeout: 30_000 })

        const links = await sidebarLinks(page)
        if (c.menu) expect(links).toEqual(c.menu)
        else expect(links.length).toBeGreaterThan(0)

        for (const path of c.forbidden) {
          if (path === "/") {
            // "/" forwards a role without dashboard access to its home. Start
            // from another permitted screen so the wait below cannot pass
            // just because we were already home.
            const elsewhere = (c.menu ?? []).find((p) => p !== c.home)
            if (elsewhere) await gotoSpa(page, elsewhere)
            await page.evaluate(() => {
              ;(window as unknown as { next: { router: { push: (p: string) => void } } }).next.router.push("/")
            })
            await page.waitForURL((url) => url.pathname === c.home)
            continue
          }
          await gotoSpa(page, path)
          await expect(page.getByTestId("access-denied")).toBeVisible()
        }

        await page.waitForLoadState("networkidle").catch(() => {})
        expect(errors, `${c.name}: API 4xx`).toEqual([])
      } finally {
        await page.context().close()
      }
    })
  }
})
