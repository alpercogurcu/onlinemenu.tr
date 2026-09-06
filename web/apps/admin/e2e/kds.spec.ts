import { expect, test } from "@playwright/test"

import { API_URL, BRANCH_ID, PRODUCT_ID, USERS, devToken, gotoSpa, loginAs } from "./fixtures/auth"

test("mutfak yeni bileti görür ve kabul eder; cihaz koyu modu sayfaya sınırlı", async ({ page, request }) => {
  const label = `E2E-${Date.now().toString(36)}`
  const { token } = await devToken(request, USERS.manager)
  const headers = { Authorization: `Bearer ${token}` }

  const check = await request.post(`${API_URL}/api/v1/pos/checks`, {
    headers,
    data: { branch_id: BRANCH_ID, table_label: label, pax: 2 },
  })
  expect(check.ok(), await check.text()).toBeTruthy()
  const checkId = ((await check.json()) as { id: string }).id

  const order = await request.post(`${API_URL}/api/v1/pos/orders`, {
    headers: { ...headers, "Idempotency-Key": `e2e-${label}` },
    data: {
      branch_id: BRANCH_ID,
      check_id: checkId,
      order_channel: "dine_in",
      items: [
        {
          product_id: PRODUCT_ID,
          product_name: "Adana Kebap",
          product_price_amount: 32000,
          product_currency: "TRY",
          tax_rate_bps: 1000,
          quantity: 1,
          unit_price_amount: 32000,
        },
      ],
    },
  })
  expect(order.ok(), await order.text()).toBeTruthy()
  const orderId = ((await order.json()) as { id: string }).id

  await loginAs(page, USERS.kitchen)
  await gotoSpa(page, "/pos/kitchen")
  await expect(page.getByText("Canlı", { exact: true })).toBeVisible()

  // Accepting is a counter decision (pos.order.accept): the kitchen sees the
  // new ticket but gets no "Kabul Et" — the counter takes it via the API.
  const card = page.locator("[data-kds-root] [data-slot=card]").filter({ hasText: label }).first()
  await card.scrollIntoViewIfNeeded()
  await expect(card).toBeVisible()
  await expect(card.getByText("Kasa onayı bekleniyor")).toBeVisible()
  await expect(card.getByRole("button", { name: "Kabul Et" })).toHaveCount(0)

  const accept = await request.post(`${API_URL}/api/v1/pos/orders/${orderId}/accept`, { headers })
  expect(accept.ok(), await accept.text()).toBeTruthy()

  // The card re-mounts in the "Kabul Edildi" column when the stream delivers
  // the accept — wait on the new button rather than the old element.
  const startButton = card.getByRole("button", { name: "Hazırlamaya Başla" })
  await expect(startButton).toBeVisible()
  await startButton.click()
  await expect(card.getByRole("button", { name: "Hazır", exact: true })).toBeVisible()

  // The switch is an sr-only checkbox behind a styled label — toggle it by
  // clicking its label, which is the only pointer target a user gets too.
  const root = page.locator("[data-kds-root]")
  const deviceDark = page.getByRole("switch", { name: "Bu cihazda koyu mod" })
  const deviceDarkLabel = page.locator("label").filter({ has: deviceDark })
  await expect(root).not.toHaveClass(/\bdark\b/)
  await deviceDarkLabel.scrollIntoViewIfNeeded()
  await deviceDarkLabel.click()
  await expect(deviceDark).toBeChecked()
  await expect(root).toHaveClass(/\bdark\b/)
  await expect(page.locator("html")).not.toHaveClass(/\bdark\b/)
  await deviceDarkLabel.click()
  await expect(deviceDark).not.toBeChecked()
  await expect(root).not.toHaveClass(/\bdark\b/)
})
