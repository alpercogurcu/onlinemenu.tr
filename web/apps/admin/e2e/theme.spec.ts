import { expect, test } from "@playwright/test"

import { USERS, gotoSpa, loginAs } from "./fixtures/auth"

test("koyu tema html'e uygulanır, yenilemede kalır; KDS dışında yerel .dark yok", async ({ page }) => {
  await loginAs(page, USERS.manager)
  const html = page.locator("html")
  await expect(html).not.toHaveClass(/\bdark\b/)

  // The account menu is mounted both in the header and the sidebar footer.
  await page.getByRole("button", { name: "Hesap menüsü" }).first().click()
  await page.getByRole("menuitem", { name: "Koyu Tema" }).click()
  await expect(html).toHaveClass(/\bdark\b/)

  await gotoSpa(page, "/catalog/products")
  await expect(page.locator("main .dark")).toHaveCount(0)

  // next-themes persists the choice in localStorage — it must survive a
  // reload even though the in-memory session does not.
  await page.reload()
  await expect(html).toHaveClass(/\bdark\b/)

  // storageKey of the ThemeProvider in app/layout.tsx.
  await page.evaluate(() => localStorage.setItem("onlinemenu-theme", "light"))
  await page.reload()
  await expect(html).not.toHaveClass(/\bdark\b/)
})
