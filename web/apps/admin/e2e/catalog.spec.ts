import { expect, test } from "@playwright/test"

import { PRODUCT_ID, USERS, gotoSpa, loginAs } from "./fixtures/auth"

// Product → option group → three options with the Enter chain → back to the
// product → delete both. Leaves the catalog as it found it.
test("ürün ve seçenek grubu turu", async ({ page }) => {
  const stamp = Date.now().toString(36)
  const productName = `E2E Ürün ${stamp}`
  const groupName = `E2E Grup ${stamp}`

  await loginAs(page, USERS.manager)

  await gotoSpa(page, "/catalog/products")
  await page.getByRole("button", { name: "Ürün ekle" }).click()
  await page.waitForURL((url) => url.pathname === "/catalog/products/new")
  await page.locator("#product-name").fill(productName)
  await page.locator("#product-price").fill("125")
  await page.getByRole("button", { name: "Kaydet" }).click()
  await page.waitForURL((url) => /^\/catalog\/products\/[0-9a-f-]{36}$/.test(url.pathname))
  await expect(page.getByRole("heading", { name: productName })).toBeVisible()
  const productUrl = new URL(page.url()).pathname

  await gotoSpa(page, "/catalog/modifiers")
  await page.getByRole("button", { name: "Grup ekle" }).click()
  await page.waitForURL((url) => url.pathname === "/catalog/modifiers/new")
  await page.locator("#group-name").fill(groupName)
  await page.getByRole("button", { name: "Kaydet" }).click()
  await page.waitForURL((url) => /^\/catalog\/modifiers\/[0-9a-f-]{36}$/.test(url.pathname))

  const add = page.getByPlaceholder("Seçenek adı")
  const optionNames = page.locator('input[id^="option-name-"]')
  const options = ["Küçük", "Orta", "Büyük"]
  for (const [i, option] of options.entries()) {
    await add.fill(option)
    await add.press("Enter")
    await expect(optionNames).toHaveCount(i + 1)
  }
  await expect(optionNames.last()).toHaveValue("Büyük")
  expect(await optionNames.evaluateAll((els) => els.map((el) => (el as HTMLInputElement).value))).toEqual(options)

  await page.getByRole("button", { name: "Sil" }).first().click()
  await page.getByRole("alertdialog").getByRole("button", { name: "Sil" }).click()
  await page.waitForURL((url) => url.pathname === "/catalog/modifiers")
  await expect(page.getByText(groupName)).toHaveCount(0)

  await gotoSpa(page, productUrl)
  await expect(page.getByRole("heading", { name: productName })).toBeVisible()
  await page.getByRole("button", { name: "Sil" }).first().click()
  await page.getByRole("alertdialog").getByRole("button", { name: "Sil" }).click()
  await page.waitForURL((url) => url.pathname === "/catalog/products")
  await expect(page.getByText(productName)).toHaveCount(0)
})

// Menu content lives on its own page (/catalog/menus/[id]), not in a dialog:
// list → "Kalemler" link → page, then add / edit-price / remove through the
// item form dialog. The product is created and deleted by the test, and the
// item is removed before the product goes, so the menu ends as it began.
test("menü içeriği kendi sayfasında yönetilir", async ({ page }) => {
  const stamp = Date.now().toString(36)
  const productName = `E2E Menü Ürünü ${stamp}`

  await loginAs(page, USERS.manager)

  await gotoSpa(page, "/catalog/products/new")
  await page.locator("#product-name").fill(productName)
  await page.locator("#product-price").fill("125")
  await page.getByRole("button", { name: "Kaydet" }).click()
  await page.waitForURL((url) => /^\/catalog\/products\/[0-9a-f-]{36}$/.test(url.pathname))
  const productUrl = new URL(page.url()).pathname

  await gotoSpa(page, "/catalog/menus")
  const manageLink = page.getByRole("table").getByRole("link", { name: "Kalemler" }).first()
  await expect(manageLink).toBeVisible()
  await manageLink.click()
  await page.waitForURL((url) => /^\/catalog\/menus\/[0-9a-f-]{36}$/.test(url.pathname))
  await expect(page.getByRole("link", { name: "Menülere dön" })).toBeVisible()
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible()
  await expect(page.getByRole("dialog")).toHaveCount(0)

  await page.getByRole("button", { name: "Kalem ekle" }).first().click()
  const addDialog = page.getByRole("dialog")
  const optionValue = await addDialog
    .getByLabel("Ürün")
    .locator("option", { hasText: productName })
    .getAttribute("value")
  expect(optionValue, "new product is offered in the picker").toBeTruthy()
  await addDialog.getByLabel("Ürün").selectOption(optionValue!)
  await addDialog.getByRole("button", { name: "Menüye ekle" }).click()
  await expect(addDialog).toHaveCount(0)

  const row = page.getByRole("row", { name: new RegExp(productName) })
  await expect(row).toBeVisible()
  await expect(row).toContainText("₺125,00")

  await row.getByRole("button", { name: "Düzenle" }).click()
  const editDialog = page.getByRole("dialog")
  await expect(editDialog.getByLabel("Ürün")).toBeDisabled()
  await editDialog.getByLabel("Menüye özel fiyat (₺)").fill("99,90")
  await editDialog.getByRole("button", { name: "Kalemi güncelle" }).click()
  await expect(editDialog).toHaveCount(0)
  await expect(row).toContainText("₺99,90")

  await row.getByRole("button", { name: `${productName} ürününü menüden çıkar` }).click()
  await expect(page.getByText(productName)).toHaveCount(0)

  await gotoSpa(page, productUrl)
  await page.getByRole("button", { name: "Sil" }).first().click()
  await page.getByRole("alertdialog").getByRole("button", { name: "Sil" }).click()
  await page.waitForURL((url) => url.pathname === "/catalog/products")
})

// Below the xl breakpoint the editor is a single column; at 1366px the two
// columns must both fit the content area without pushing the page sideways.
test("ürün editörü 1366x768'de yatay taşmaz", async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 768 })
  await loginAs(page, USERS.manager)

  await gotoSpa(page, `/catalog/products/${PRODUCT_ID}`)
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible()
  await expect(page.getByText("Seçenek grupları").first()).toBeVisible()

  const overflow = await page.evaluate(() => {
    const doc = document.documentElement
    const cards = Array.from(document.querySelectorAll<HTMLElement>("main [data-slot='card']"))
    return {
      page: doc.scrollWidth - doc.clientWidth,
      cards: cards.filter((c) => c.scrollWidth > c.clientWidth).length,
      cardsRight: Math.max(...cards.map((c) => c.getBoundingClientRect().right)),
      viewport: doc.clientWidth,
    }
  })
  expect(overflow.page, "page scrolls horizontally").toBeLessThanOrEqual(0)
  expect(overflow.cards, "a card clips its own content").toBe(0)
  expect(overflow.cardsRight).toBeLessThanOrEqual(overflow.viewport)
})
