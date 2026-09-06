import { expect, test } from "@playwright/test"

import { USERS, gotoSpa, loginAs } from "./fixtures/auth"

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
