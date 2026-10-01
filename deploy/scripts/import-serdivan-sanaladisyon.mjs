// Sanal Adisyon (Serdivan/Merkez) kataloğunu + masa planını prod'a aktarır.
// Kaynak veri: sanaladisyon-serdivan.json (2026-10-01 panel dökümü — geçiş günü
// legacy'de fiyat değiştiyse JSON güncellenip yeniden koşulur).
// Kullanım: set -a; . deploy/.env.diverserver.local; set +a
//          node deploy/scripts/import-serdivan-sanaladisyon.mjs state|dry|apply
// Ad eşlemesiyle idempotent: var olanı atlar, eksikse oluşturur; yeniden koşmak güvenlidir.
import fs from "node:fs"

const API = "https://api.diverstreetfood.com"
const env = (k, d) => process.env[k] ?? d
const DATA = JSON.parse(
  fs.readFileSync(
    new URL("./sanaladisyon-serdivan.json", import.meta.url),
    "utf8",
  ),
)
const MODE = process.argv[2] ?? "dry"
const APPLY = MODE === "apply"

// Yeni burger ürünleri legacy'de tek BURGERLER kategorisinde; prod'daki ince
// ayrımı (Burgerler) koruyoruz — yeni double'lar Burgerler'e gider.
const NEW_PRODUCT_CATEGORY_OVERRIDE = { }

async function principal(email, password) {
  const form = new URLSearchParams({
    grant_type: "password",
    client_id: env("E2E_PROD_CLIENT_ID", "e2e-prod"),
    client_secret: env("E2E_PROD_CLIENT_SECRET"),
    username: email,
    password,
  })
  const kc = await fetch(
    `https://auth.diverstreetfood.com/realms/${env("E2E_PROD_REALM", "onlinemenu")}/protocol/openid-connect/token`,
    { method: "POST", body: form },
  ).then((r) => r.json())
  const auth = { Authorization: `Bearer ${kc.access_token}` }
  const ctxs = await fetch(`${API}/v1/identity/me/contexts`, { headers: auth }).then((r) => r.json())
  const ctx = ctxs.contexts[0]
  const { token } = await fetch(`${API}/v1/identity/auth/context`, {
    method: "POST",
    headers: { ...auth, "Content-Type": "application/json" },
    body: JSON.stringify({ membership_id: ctx.membership_id }),
  }).then((r) => r.json())
  return { ctx, h: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" } }
}

const m = await principal(env("TEST_YONETICI_EMAIL"), env("TEST_YONETICI_PASSWORD"))
const H = m.h

async function get(path) {
  const r = await fetch(`${API}${path}`, { headers: H })
  if (!r.ok) throw new Error(`GET ${path} -> ${r.status}: ${await r.text()}`)
  return r.json()
}
async function send(method, path, body, idem) {
  if (!APPLY) {
    console.log(`  [dry] ${method} ${path} ${body ? JSON.stringify(body) : ""}`)
    return null
  }
  const headers = idem ? { ...H, "Idempotency-Key": idem } : H
  const r = await fetch(`${API}${path}`, { method, headers, body: body ? JSON.stringify(body) : undefined })
  const text = await r.text()
  if (!r.ok) throw new Error(`${method} ${path} -> ${r.status}: ${text}`)
  console.log(`  [ok]  ${method} ${path} -> ${r.status}`)
  return text ? JSON.parse(text) : null
}

const branches = await get(`/tenants/${m.ctx.tenant_id}/branches/`)
const serdivan = branches.find((b) => b.slug === "serdivan")
if (!serdivan) throw new Error("serdivan şubesi yok")

const categories = await get("/api/v1/catalog/categories")
const products = await get("/api/v1/catalog/products")
const overrides = await get(`/api/v1/catalog/branches/${serdivan.id}/product-overrides`)
const menus = await get("/api/v1/catalog/menus")
const anaMenu = menus.find((mm) => mm.is_active)
const menuItems = anaMenu ? await get(`/api/v1/catalog/menus/${anaMenu.id}/items`) : []
const zones = await get(`/api/v1/pos/zones?branch_id=${serdivan.id}`)
const plan = await get(`/api/v1/pos/tables?branch_id=${serdivan.id}`)
const qrCodes = await get(`/api/v1/storefront/qr-codes?branch_id=${serdivan.id}`)

if (MODE === "state") {
  console.log("KATEGORILER:", categories.map((c) => `${c.name}${c.is_active === false ? "(pasif)" : ""}`).join(" | "))
  console.log("URUNLER:", products.length)
  for (const p of products) console.log(`  ${p.name} — ${p.price_amount / 100} TL, kdv ${p.tax_rate_bps}bps${p.is_active === false ? " (pasif)" : ""}`)
  console.log("SERDIVAN OVERRIDELAR:", overrides.length, JSON.stringify(overrides).slice(0, 300))
  console.log("MENULER:", menus.map((x) => `${x.name}${x.is_active ? "(aktif)" : ""}`).join(" | "), "— Ana menü kalem:", menuItems.length)
  console.log("ZONLAR:", JSON.stringify(zones))
  for (const z of plan) console.log(`BOLGE ${z.name}: ${z.tables.map((t) => `${t.name}[${t.status}]`).join(", ")}`)
  console.log("QR:", qrCodes.length, "aktif:", qrCodes.filter((q) => q.status === "active").length)
  process.exit(0)
}

const byName = (list) => Object.fromEntries(list.map((x) => [x.name.trim().toLocaleLowerCase("tr"), x]))
const catBy = byName(categories)
const prodBy = byName(products)
const ovByProduct = Object.fromEntries(overrides.map((o) => [o.product_id, o]))
const menuProductIds = new Set(menuItems.map((i) => i.product_id))

console.log(`== PLAN (${MODE}) — Serdivan ${serdivan.id} ==`)

// 1) Kategoriler
const catIds = {}
for (const cat of DATA.categories) {
  const hit = catBy[cat.name.toLocaleLowerCase("tr")]
  if (hit) {
    catIds[cat.name] = hit.id
    console.log(`kategori var: ${cat.name}`)
  } else {
    console.log(`kategori YOK -> olusturulacak: ${cat.name}`)
    const created = await send("POST", "/api/v1/catalog/categories", { name: cat.name, description: "", sort_order: 0 })
    if (created) catIds[cat.name] = created.id
  }
}

// 2) Ürünler + Serdivan fiyatı
const summary = { newProducts: 0, overrides: 0, priceMatches: 0, menuAdds: 0, menuRemoves: 0 }
for (const cat of DATA.categories) {
  for (const item of cat.products) {
    const priceKurus = Math.round(item.price * 100)
    const existing = prodBy[item.name.toLocaleLowerCase("tr")]
    if (item.existing && !existing) throw new Error(`EŞLEŞME HATASI: prod'da bulunamadı: ${item.name}`)
    if (existing) {
      const ov = ovByProduct[existing.id]
      const effective = ov?.price_amount ?? existing.price_amount
      if (effective === priceKurus) {
        summary.priceMatches++
      } else {
        console.log(`fiyat farkı: ${item.name} prod=${effective / 100} legacy=${item.price} -> Serdivan override`)
        await send("PUT", `/api/v1/catalog/branches/${serdivan.id}/products/${existing.id}/override`, {
          price_amount: priceKurus,
          is_available: true,
        })
        summary.overrides++
      }
      if (anaMenu && item.guest_menu !== false && !menuProductIds.has(existing.id)) {
        console.log(`menüye eklenecek: ${item.name}`)
        await send("POST", `/api/v1/catalog/menus/${anaMenu.id}/items`, { product_id: existing.id, is_active: true, sort_order: 0 })
        summary.menuAdds++
      }
      continue
    }
    console.log(`yeni ürün: ${item.name} — ${item.price} TL (${cat.name})`)
    summary.newProducts++
    const created = await send("POST", "/api/v1/catalog/products", {
      category_id: catIds[cat.name] ?? null,
      name: item.name,
      description: "",
      price_amount: priceKurus,
      currency: "TRY",
      unit: "adet",
      tax_rate_bps: DATA.kdv_percent * 100,
      sort_order: 0,
    })
    if (created) {
      prodBy[item.name.toLocaleLowerCase("tr")] = created
      if (anaMenu) {
        const freshItems = await get(`/api/v1/catalog/menus/${anaMenu.id}/items`)
        const inMenu = freshItems.some((i) => i.product_id === created.id)
        if (item.guest_menu === false && inMenu) {
          console.log(`  menüden çıkarılacak (misafire gösterilmez): ${item.name}`)
          await send("DELETE", `/api/v1/catalog/menus/${anaMenu.id}/items/${created.id}`)
          summary.menuRemoves++
        } else if (item.guest_menu !== false && !inMenu) {
          await send("POST", `/api/v1/catalog/menus/${anaMenu.id}/items`, { product_id: created.id, is_active: true, sort_order: 0 })
          summary.menuAdds++
        }
      }
    } else if (item.guest_menu === false) {
      console.log(`  [dry] (oluşunca menüde olup olmadığı kontrol edilip çıkarılacak)`)
    }
  }
}

// 3) Masa planı
const zoneBy = byName(zones)
const existingTables = new Set(plan.flatMap((z) => z.tables).map((t) => t.name.trim().toLocaleLowerCase("tr")))
const qrByTable = Object.fromEntries(qrCodes.filter((q) => q.status === "active").map((q) => [q.table_id, q]))
const createdTables = []
for (const zone of DATA.zones) {
  let zoneId = zoneBy[zone.name.toLocaleLowerCase("tr")]?.id
  if (!zoneId) {
    console.log(`bölge YOK -> olusturulacak: ${zone.name}`)
    const created = await send("POST", "/api/v1/pos/zones", { branch_id: serdivan.id, name: zone.name, floor: 0 })
    if (created) zoneId = created.id
  } else {
    console.log(`bölge var: ${zone.name}`)
  }
  for (const label of zone.tables) {
    if (existingTables.has(label.toLocaleLowerCase("tr"))) {
      console.log(`  masa var: ${label}`)
      continue
    }
    console.log(`  masa olusturulacak: ${label}${zone.qr ? " (+QR)" : ""}`)
    const t = await send("POST", "/api/v1/pos/tables", {
      branch_id: serdivan.id,
      zone_id: zoneId,
      name: label,
      capacity: 4,
    })
    if (t && zone.qr) {
      createdTables.push(t)
      await send("POST", "/api/v1/storefront/qr-codes", { branch_id: serdivan.id, table_id: t.id, table_label: label })
    }
  }
}

// 4) Test artığı "Salon" bölgesi (Masa 1-3) gerçek planı kirletmesin diye
// pasifleştirilir — silinmez: adisyon geçmişi ve QR kayıtları üstünde durur.
const salon = zones.find((z) => z.name === "Salon" && z.is_active)
if (salon) {
  const salonTables = plan.find((z) => z.id === salon.id)?.tables ?? plan.flatMap((z) => z.tables).filter((t) => t.zone_id === salon.id)
  console.log(`eski Salon bölgesi pasifleştirilecek (${salonTables.length} masa)`)
  for (const t of salonTables) {
    await send("PATCH", `/api/v1/pos/tables/${t.id}`, { is_active: false })
  }
  await send("PATCH", `/api/v1/pos/zones/${salon.id}`, { is_active: false })
}

console.log("OZET:", JSON.stringify(summary))
console.log(APPLY ? "UYGULANDI" : "DRY-RUN — hiçbir şey yazılmadı")
