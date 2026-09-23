// Frontend mirror of backend/internal/platform/httpx/route_guard_coverage_test.go:
// a screen cannot exist without a reviewed permission entry, and no permission
// string used by the UI may drift away from the generated OPA matrix.
import fs from "node:fs"
import path from "node:path"

import { describe, expect, it } from "vitest"

import { getSidebarSections } from "@/components/layouts/sidebar-menu-config"
import { isKnownAction } from "@/lib/permissions"
import {
  ROUTE_PERMISSIONS,
  floorPlanRouteFor,
  homeRouteFor,
  matchRoute,
  rolesCanAccess,
} from "@/lib/route-permissions"

const SRC = path.resolve(process.cwd(), "src")
const MAIN = path.join(SRC, "app", "(main)")

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) walk(full, out)
    else out.push(full)
  }
  return out
}

// app/(main)/catalog/products/[id]/page.tsx -> /catalog/products/[id]
// Route groups "(x)" do not appear in the URL.
function pageToPattern(file: string): string {
  const rel = path.relative(MAIN, path.dirname(file)).split(path.sep)
  const segments = rel.filter((s) => s !== "" && !/^\(.*\)$/.test(s))
  return "/" + segments.join("/")
}

describe("route permission registry coverage", () => {
  const pages = walk(MAIN)
    .filter((f) => path.basename(f) === "page.tsx")
    .map(pageToPattern)
    .sort()

  it("has exactly one entry per app/(main) page", () => {
    expect(Object.keys(ROUTE_PERMISSIONS).sort()).toEqual(pages)
  })

  it("only names actions that exist in the generated OPA matrix", () => {
    for (const [route, action] of Object.entries(ROUTE_PERMISSIONS)) {
      expect(isKnownAction(action), `${route} -> ${action}`).toBe(true)
    }
  })

  it("every can()/useCan() literal in src exists in the matrix (typo = hidden for everyone)", () => {
    const literal = /\b(?:useCan|can)\(\s*"([^"]+)"\s*\)/g
    const found: string[] = []
    for (const file of walk(SRC)) {
      if (!/\.(ts|tsx)$/.test(file) || file.includes(`${path.sep}test${path.sep}`)) continue
      const text = fs.readFileSync(file, "utf8")
      for (const m of text.matchAll(literal)) found.push(`${path.relative(SRC, file)}: ${m[1]}`)
    }
    expect(found.length).toBeGreaterThan(0)
    const unknown = found.filter((entry) => !isKnownAction(entry.split(": ")[1]))
    expect(unknown).toEqual([])
  })
})

describe("matchRoute", () => {
  it.each([
    ["/", "/"],
    ["/pos/checks", "/pos/checks"],
    ["/pos/checks/", "/pos/checks"],
    ["/pos/checks/0b8f", "/pos/checks/[id]"],
    ["/catalog/products/new", "/catalog/products/new"],
    ["/catalog/products/abc", "/catalog/products/[id]"],
    ["/catalog/products?q=x", "/catalog/products"],
    ["/pos/order?branch=b&table=t", "/pos/order"],
  ])("%s -> %s", (pathname, pattern) => {
    expect(matchRoute(pathname)).toBe(pattern)
  })

  it("does not match unregistered paths (fail closed)", () => {
    expect(matchRoute("/pos/orders")).toBeNull()
    expect(rolesCanAccess(["manager"], "/nope")).toBe(false)
  })
})

// The menu each role sees is derived from the same registry the RouteGuard
// enforces. This table is the reviewed expectation; a rego change that alters
// it fails here as well as in the e2e role suite.
const EXPECTED_MENU: Record<string, string[]> = {
  shift_manager: ["/", "/pos/tables", "/pos/checks", "/pos/kitchen", "/payment/payments"],
  cashier: ["/pos/tables", "/pos/checks", "/pos/kitchen"],
  // One floor plan: the waiter's "Masalar" is the order screen's plan.
  waiter: ["/pos/order", "/pos/checks"],
  kitchen: ["/pos/tables", "/pos/kitchen"],
  bar: ["/pos/tables", "/pos/kitchen"],
  warehouse: [
    "/inventory/warehouses",
    "/inventory/stock-items",
    "/inventory/stock-levels",
    "/inventory/movements",
    "/inventory/supply-policies",
    "/inventory/purchase-receipts",
  ],
  driver: [],
}

function menuFor(role: string): string[] {
  return getSidebarSections((k) => k, (url) => rolesCanAccess([role], url), floorPlanRouteFor([role])).flatMap((s) =>
    s.items.map((i) => i.url),
  )
}

describe("sidebar per role", () => {
  it.each(Object.entries(EXPECTED_MENU))("%s", (role, expected) => {
    expect(menuFor(role)).toEqual(expected)
  })

  it("manager sees every sidebar item", () => {
    const all = getSidebarSections((k) => k).flatMap((s) => s.items.map((i) => i.url))
    expect(menuFor("manager")).toEqual(all)
  })

  it("the catalog editor is not offered to any operational role", () => {
    for (const role of ["waiter", "cashier", "kitchen", "bar", "shift_manager"]) {
      expect(menuFor(role).some((u) => u.startsWith("/catalog")), role).toBe(false)
    }
  })
})

describe("web order screen", () => {
  it("is open to every role that may place an order, and only to them", () => {
    for (const role of ["manager", "shift_manager", "cashier", "waiter"]) {
      expect(rolesCanAccess([role], "/pos/order?table=x"), role).toBe(true)
    }
    for (const role of ["kitchen", "bar", "warehouse", "driver"]) {
      expect(rolesCanAccess([role], "/pos/order"), role).toBe(false)
    }
  })
})

describe("floorPlanRouteFor", () => {
  it.each([
    [["waiter"], "/pos/order"],
    [["cashier"], "/pos/tables"],
    [["shift_manager"], "/pos/tables"],
    [["manager"], "/pos/tables"],
    [["kitchen"], "/pos/tables"],
    [["bar"], "/pos/tables"],
    [["waiter", "cashier"], "/pos/tables"],
  ])("%j -> %s", (roles, route) => {
    expect(floorPlanRouteFor(roles)).toBe(route)
  })
})

describe("homeRouteFor", () => {
  it.each([
    [["manager"], "/"],
    [["shift_manager"], "/"],
    [["cashier"], "/pos/checks"],
    [["waiter"], "/pos/order"],
    [["kitchen"], "/pos/kitchen"],
    [["bar"], "/pos/kitchen"],
    [["warehouse"], "/inventory/stock-levels"],
    [["waiter", "cashier"], "/pos/checks"],
    [["driver"], null],
    [[], null],
  ])("%j -> %s", (roles, home) => {
    expect(homeRouteFor(roles)).toBe(home)
  })

  it("every home is a screen the role may open", () => {
    for (const role of ["manager", "shift_manager", "cashier", "waiter", "kitchen", "bar", "warehouse"]) {
      const home = homeRouteFor([role])
      expect(home, role).not.toBeNull()
      expect(rolesCanAccess([role], home as string), role).toBe(true)
    }
  })
})
