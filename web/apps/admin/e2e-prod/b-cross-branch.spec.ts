// (b) Çapraz şube erişimi (ADR-SEC-005 + 2026-09-20 çapraz şube ödeme açığı).
// Bir şubenin kasiyeri başka şubenin adisyonunu ne görebilmeli ne de
// ödeyebilmelidir.

import { expect, test } from "@playwright/test"

import {
  ACCOUNTS,
  RUN,
  type Check,
  cashSale,
  cleanupCheck,
  expectStatus,
  json,
  openCheck,
  principal,
} from "./fixtures/prod"

test.describe.configure({ mode: "serial" })

test("(b) İzmit kasiyeri Serdivan adisyonunu göremez, ödeyemez", async ({ request }) => {
  const serdivan = await principal(request, ACCOUNTS.cashierSerdivan())
  const izmit = await principal(request, ACCOUNTS.cashierIzmit())
  const kitchen = await principal(request, ACCOUNTS.kitchenSerdivan())
  const serdivanBranch = serdivan.ctx.branch_id as string
  const izmitBranch = izmit.ctx.branch_id as string
  expect(serdivanBranch).not.toBe(izmitBranch)

  const check = await openCheck(serdivan.api, serdivanBranch, `${RUN}-b-serdivan`)
  try {
    // Tekil okuma: yabancı adisyon "yok" gibi davranmalı, "yasak" değil —
    // 404 varlık sızdırmaz.
    await expectStatus(await izmit.api.get(`/api/v1/pos/checks/${check.id}`), 404)

    // Liste: ya reddedilir ya da süzülür; her iki durumda yabancı adisyon görünmez.
    const listRes = await izmit.api.get(`/api/v1/pos/checks?branch_id=${serdivanBranch}`)
    expect([403, 422]).toContain(listRes.status())

    const ownList = await json<Check[]>(await izmit.api.get(`/api/v1/pos/checks?branch_id=${izmitBranch}`), 200)
    expect(ownList.map((c) => c.id)).not.toContain(check.id)

    // Ödeme: şube uyuşmazlığı reddedilmeli.
    const pay = await izmit.api.postNew("/api/v1/payments", cashSale(izmitBranch, check.id, "PRODTEST çapraz", 10_000))
    expect([403, 404, 409, 422]).toContain(pay.status())
    const body = await pay.text()
    expect(body, `çapraz şube ödemesi reddedilmeli, gövde: ${body}`).toMatch(
      /branch_forbidden|check_branch_mismatch|forbidden|not_found/,
    )

    // Serdivan kasiyeri kendi adisyonunu görebiliyor olmalı (kontrol grubu).
    const own = await json<Check>(await serdivan.api.get(`/api/v1/pos/checks/${check.id}`), 200)
    expect(own.id).toBe(check.id)
  } finally {
    await cleanupCheck(serdivan.api, kitchen.api, check.id)
  }
})
