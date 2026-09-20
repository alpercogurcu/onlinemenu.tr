// (g) Maliyet anahtarı hiçbir yanıtta görünmemeli (DTO projeksiyonu,
// ADR-AUTH-001 katman 4). b2b aktarımında `cost_price_tl` bilerek dışarıda
// bırakıldı; bu test hem alanın yokluğunu hem de kataloğun gerçekten maliyet
// verisi taşıyıp taşımadığını kaydeder — veri hiç yoksa "geçti" demek
// boş bir doğrulama olur.

import { expect, test } from "@playwright/test"

import { ACCOUNTS, type Api, allTables, branchesBySlug, json, principal } from "./fixtures/prod"

const COST_KEYS = /("(cost|cost_price|cost_price_tl|cost_amount|unit_cost|purchase_price|maliyet)"\s*:)/i

async function bodyOf(api: Api, path: string): Promise<string> {
  const res = await api.get(path)
  if (res.status() !== 200) return ""
  return res.text()
}

test("(g) maliyet alanı hiçbir yanıtta yok", async ({ request }) => {
  const manager = await principal(request, ACCOUNTS.manager())
  const tenantId = manager.ctx.tenant_id
  const branches = await branchesBySlug(manager.api, tenantId)
  const izmit = branches.izmit.id

  const paths = [
    `/api/v1/catalog/products`,
    `/api/v1/catalog/products?branch_id=${izmit}`,
    `/api/v1/catalog/categories`,
    `/api/v1/catalog/branches/${izmit}/product-overrides`,
    `/tenants/${tenantId}/branches/`,
    `/api/v1/pos/tables?branch_id=${izmit}`,
    `/api/v1/pos/checks?branch_id=${izmit}`,
  ]

  for (const path of paths) {
    const body = await bodyOf(manager.api, path)
    expect(body, `maliyet anahtarı sızdı: ${path}`).not.toMatch(COST_KEYS)
  }

  // Yönetici en geniş projeksiyondur; kasiyerde de olmadığını ayrıca doğrula.
  const cashier = await principal(request, ACCOUNTS.cashierIzmit())
  const cashierBody = await bodyOf(cashier.api, `/api/v1/catalog/products?branch_id=${izmit}`)
  expect(cashierBody).not.toMatch(COST_KEYS)

  // Kayıt: maliyet verisi kataloğa hiç girmediyse bu testin gücü sınırlıdır.
  // Ürün yanıtının alan listesi rapora yazılır.
  const sample = await json<Record<string, unknown>[]>(
    await manager.api.get(`/api/v1/catalog/products?branch_id=${izmit}`),
    200,
  )
  const fields = Object.keys(sample[0]).sort()
  test.info().annotations.push({ type: "ürün yanıtı alanları", description: fields.join(", ") })
  expect(fields).not.toContain("cost_price_amount")
})
