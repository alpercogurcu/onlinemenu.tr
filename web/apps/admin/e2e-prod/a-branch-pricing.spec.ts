// (a) Şube fiyat override'ı (ADR-DATA-009) canlı veriyle.
//
// b2b aktarımı İzmit ve Kırkpınar'a 10'ar şube fiyatı yazdı; tenant fiyatı
// b2b tabanıdır. American Smash Burger: tenant 470 TL, İzmit 490 TL.

import { expect, test } from "@playwright/test"

import {
  ACCOUNTS,
  type Api,
  RUN,
  branchesBySlug,
  cleanupCheck,
  json,
  line,
  openCheck,
  placeOrder,
  principal,
  productByName,
  products,
} from "./fixtures/prod"

const SMASH = "American Smash Burger"

test.describe.configure({ mode: "serial" })

test.describe("(a) şube fiyatı", () => {
  test("İzmit kasiyeri 490 TL görür ve o fiyatla satar; 470 TL 422 price_mismatch", async ({ request }) => {
    const izmit = await principal(request, ACCOUNTS.cashierIzmit())
    const branchId = izmit.ctx.branch_id as string
    expect(branchId, "İzmit kasiyerinin üyeliği şube kapsamlı olmalı (SEC-005)").toBeTruthy()

    const product = await productByName(izmit.api, SMASH, branchId)
    expect(product.price_amount, "İzmit override fiyatı 490 TL olmalı").toBe(49_000)
    expect(product.branch_price_overridden).toBe(true)

    const check = await openCheck(izmit.api, branchId, `${RUN}-a-izmit`)
    try {
      // Doğru fiyat geçer.
      const ok = await placeOrder(izmit.api, branchId, check.id, [line(product)])
      expect(ok.status(), await ok.text()).toBe(201)

      // Tenant fiyatı (470) bu şubede geçersiz: sipariş ucu katalogdan yeniden
      // fiyatlandırır ve reddeder.
      const mismatch = await placeOrder(izmit.api, branchId, check.id, [line(product, 1, 47_000)])
      expect(mismatch.status(), await mismatch.text()).toBe(422)
      expect(await mismatch.json()).toMatchObject({ code: "price_mismatch" })
    } finally {
      const kitchen = await principal(request, ACCOUNTS.kitchenIzmit())
      await cleanupCheck(izmit.api, kitchen.api, check.id)
    }
  })

  test("Serdivan kasiyeri aynı ürünü tenant fiyatıyla (470 TL) görür", async ({ request }) => {
    const serdivan = await principal(request, ACCOUNTS.cashierSerdivan())
    const product = await productByName(serdivan.api, SMASH, serdivan.ctx.branch_id as string)
    expect(product.price_amount).toBe(47_000)
    expect(product.branch_price_overridden).toBe(false)
  })

  test("branch_id verilmezse şube kapsamlı kasiyer KENDİ şube fiyatını görür (B3)", async ({ request }) => {
    // `?branch_id=` hâlâ opsiyoneldir, ama artık "tenant varsayılanı" demek
    // değil: OPA kapsamı "tenant" olmayan bir personel kendi şubesine düşer
    // (catalog/http/branch_override_handler.go defaultBranchID). Parametreyi
    // unutan bir POS istemcisi böylece ekranda 470 gösterip siparişte 422
    // price_mismatch yemez.
    const izmit = await principal(request, ACCOUNTS.cashierIzmit())
    const withoutBranch = (await products(izmit.api)).find((p) => p.name === SMASH)
    expect(withoutBranch?.price_amount, "İzmit kasiyeri parametresiz de 490 TL görmeli").toBe(49_000)
    expect(withoutBranch?.branch_price_overridden).toBe(true)

    // Override'ı olmayan şubede aynı çağrı tenant fiyatını verir (fark,
    // varsayılanın değişmesi değil, şubenin override'ı olmaması).
    const serdivan = await principal(request, ACCOUNTS.cashierSerdivan())
    const serdivanDefault = (await products(serdivan.api)).find((p) => p.name === SMASH)
    expect(serdivanDefault?.price_amount).toBe(47_000)
    expect(serdivanDefault?.branch_price_overridden).toBe(false)

    // Zincir geneli yönetici (kapsam "tenant") ADR-DATA-009 öncesindeki
    // davranışta kalır: parametresiz çağrı tenant fiyatıdır.
    const manager = await principal(request, ACCOUNTS.manager())
    const managerDefault = (await products(manager.api)).find((p) => p.name === SMASH)
    expect(managerDefault?.price_amount, "yönetici varsayılanı tenant fiyatı olmalı").toBe(47_000)
    expect(managerDefault?.branch_price_overridden).toBe(false)
  })

  test("garson katalog okuyamaz (OPA catalog_read_actions garsonu kapsamaz)", async ({ request }) => {
    // Kayıt: `catalog_read_actions` yalnız cashier/shift_manager/kitchen/bar
    // içerir (backend/configs/opa/bundles/authz.rego:86-98) ve garsonun tek
    // izni `tables:read`'tir (migrations/identity/000017). İki katman da aynı
    // yönde reddediyor; bu test davranışı sabitler, doğru olduğunu iddia etmez
    // — garsonun menü okuması gerekiyorsa bu bir rego + seed kararıdır.
    const waiter = await principal(request, ACCOUNTS.waiterSerdivan())
    for (const path of ["/api/v1/catalog/categories", "/api/v1/catalog/products", "/api/v1/catalog/menus"]) {
      const res = await waiter.api.get(path)
      expect(res.status(), `${path} garsona kapalı olmalı`).toBe(403)
    }
    // Kontrol grubu: masa planını okuyabiliyor.
    expect((await waiter.api.get(`/api/v1/pos/tables?branch_id=${waiter.ctx.branch_id}`)).status()).toBe(200)
  })

  test("Kırkpınar'da 10, Adapazarı'nda 0 şube fiyatı vardır", async ({ request }) => {
    const manager = await principal(request, ACCOUNTS.manager())
    const branches = await branchesBySlug(manager.api, manager.ctx.tenant_id)
    const counts: Record<string, number> = {}
    for (const slug of ["izmit", "kirkpinar", "adapazari", "serdivan"]) {
      const list = await products(manager.api, branches[slug].id)
      counts[slug] = list.filter((p) => p.branch_price_overridden).length
    }
    expect(counts).toEqual({ izmit: 10, kirkpinar: 10, adapazari: 0, serdivan: 0 })
  })
})
