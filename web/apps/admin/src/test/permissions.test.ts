import { afterEach, describe, expect, it } from "vitest"

import matrix from "@/generated/permission-matrix.json"
import { clearAccessToken, setAccessToken } from "@/lib/api"
import { can, currentRoleNames, isKnownAction } from "@/lib/permissions"

const ROLE_IDS = {
  cashier: "00000001-0000-0000-0000-000000000001",
  shift_manager: "00000001-0000-0000-0000-000000000002",
  driver: "00000001-0000-0000-0000-000000000003",
  kitchen: "00000001-0000-0000-0000-000000000004",
  bar: "00000001-0000-0000-0000-000000000005",
  manager: "00000001-0000-0000-0000-000000000006",
  warehouse: "00000001-0000-0000-0000-000000000007",
  waiter: "00000001-0000-0000-0000-000000000008",
} as const

function base64UrlEncode(obj: unknown): string {
  return Buffer.from(JSON.stringify(obj)).toString("base64url")
}

// CTX-token-shaped string (context_token.go's 3-segment layout) carrying the
// given role ids in `rids`. lib/permissions.ts only decodes the payload.
function ctxToken(roleIds: string[]): string {
  return `${base64UrlEncode({ alg: "HS256", typ: "CTX" })}.${base64UrlEncode({ rids: roleIds })}.fake-signature`
}

describe("generated permission matrix", () => {
  it("names exactly the system roles the tests (and authz.rego) know", () => {
    expect(matrix.roles).toEqual(
      Object.fromEntries(Object.entries(ROLE_IDS).map(([name, id]) => [id, name])),
    )
  })

  it("grants the manager wildcard every action", () => {
    for (const [action, roles] of Object.entries(matrix.actions)) {
      expect(roles, action).toContain("manager")
    }
  })
})

describe("can (matrix-backed, fail-closed)", () => {
  afterEach(() => clearAccessToken())

  it("denies everything without a session token", () => {
    expect(can("pos.check.read")).toBe(false)
  })

  it("denies an action the matrix does not know — even for the manager", () => {
    setAccessToken(ctxToken([ROLE_IDS.manager]))
    expect(isKnownAction("pos.check.typo")).toBe(false)
    expect(can("pos.check.typo")).toBe(false)
  })

  it("fails closed on a malformed token", () => {
    setAccessToken("not-a-ctx-token")
    expect(can("pos.check.read")).toBe(false)
  })

  it("fails closed on an unrecognized (tenant clone / custom) role id", () => {
    setAccessToken(ctxToken(["11111111-1111-1111-1111-111111111111"]))
    expect(currentRoleNames().size).toBe(0)
    expect(can("pos.check.read")).toBe(false)
  })

  // Spot checks against authz.rego decisions that shaped the UI work. The
  // matrix itself is proven against the real engine in Go; these guard the
  // lookup, not the policy.
  it.each([
    ["waiter", "pos.order.place", true],
    ["waiter", "pos.check.close", false],
    ["waiter", "catalog.product.read", true],
    ["waiter", "catalog.product.update", false],
    ["waiter", "payment.fiscal_status.read", false],
    ["waiter", "storefront.qr.read", false],
    ["cashier", "pos.check.close", true],
    ["cashier", "pos.report.read", false],
    ["cashier", "storefront.qr.manage", false],
    ["shift_manager", "storefront.qr.manage", true],
    ["shift_manager", "pos.report.read", true],
    ["shift_manager", "catalog.branch_override.manage", false],
    ["kitchen", "pos.order.advance", true],
    ["kitchen", "pos.order.accept", false],
    ["warehouse", "inventory.supply_policy.read", true],
    ["warehouse", "inventory.supply_policy.create", false],
    ["manager", "catalog.branch_override.manage", true],
  ] as const)("%s → %s = %s", (role, action, expected) => {
    setAccessToken(ctxToken([ROLE_IDS[role]]))
    expect(can(action)).toBe(expected)
  })

  it("unions the grants of several roles", () => {
    setAccessToken(ctxToken([ROLE_IDS.waiter, ROLE_IDS.kitchen]))
    expect(can("pos.order.place")).toBe(true)
    expect(can("pos.order.advance")).toBe(true)
  })
})
