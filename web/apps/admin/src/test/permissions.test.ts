import { afterEach, describe, expect, it } from "vitest"

import { clearAccessToken, setAccessToken } from "@/lib/api"
import { can } from "@/lib/permissions"

const SYSTEM_ROLE_IDS = {
  cashier: "00000001-0000-0000-0000-000000000001",
  shiftManager: "00000001-0000-0000-0000-000000000002",
  manager: "00000001-0000-0000-0000-000000000006",
}

function base64UrlEncode(obj: unknown): string {
  return Buffer.from(JSON.stringify(obj)).toString("base64url")
}

// Builds a CTX-token-shaped string (same 3-segment base64url layout as a
// JWT — see context_token.go's `sign`) carrying the given role ids in the
// `rids` claim. Signature is a placeholder: lib/permissions.ts only reads
// the payload, exactly like decodeJwtPayload, and never verifies it (that
// happens server-side).
function ctxToken(roleIds: string[]): string {
  const header = base64UrlEncode({ alg: "HS256", typ: "CTX" })
  const payload = base64UrlEncode({ rids: roleIds })
  return `${header}.${payload}.fake-signature`
}

describe("can (cosmetic client-side permission gate)", () => {
  afterEach(() => {
    clearAccessToken()
  })

  it("denies a gated action when there is no session token", () => {
    expect(can("storefront.qr.manage")).toBe(false)
  })

  it("denies a gated action for a role without it (cashier)", () => {
    setAccessToken(ctxToken([SYSTEM_ROLE_IDS.cashier]))
    expect(can("storefront.qr.manage")).toBe(false)
  })

  it("allows a gated action for a role that holds it (shift_manager)", () => {
    setAccessToken(ctxToken([SYSTEM_ROLE_IDS.shiftManager]))
    expect(can("storefront.qr.manage")).toBe(true)
  })

  it("allows every gated action for the manager wildcard role", () => {
    setAccessToken(ctxToken([SYSTEM_ROLE_IDS.manager]))
    expect(can("storefront.qr.manage")).toBe(true)
  })

  it("allows an action that has no ACTION_ROLES entry regardless of role", () => {
    setAccessToken(ctxToken([SYSTEM_ROLE_IDS.cashier]))
    expect(can("storefront.qr.read")).toBe(true)
  })

  it("fails closed on a malformed token", () => {
    setAccessToken("not-a-ctx-token")
    expect(can("storefront.qr.manage")).toBe(false)
  })

  it("fails closed on an unrecognized (e.g. custom tenant) role id", () => {
    setAccessToken(ctxToken(["11111111-1111-1111-1111-111111111111"]))
    expect(can("storefront.qr.manage")).toBe(false)
  })
})

describe("can (pos.report.read)", () => {
  afterEach(() => {
    clearAccessToken()
  })

  it("denies the sales report for a cashier", () => {
    setAccessToken(ctxToken([SYSTEM_ROLE_IDS.cashier]))
    expect(can("pos.report.read")).toBe(false)
  })

  it("allows the sales report for a shift_manager", () => {
    setAccessToken(ctxToken([SYSTEM_ROLE_IDS.shiftManager]))
    expect(can("pos.report.read")).toBe(true)
  })

  it("allows the sales report for the manager wildcard role", () => {
    setAccessToken(ctxToken([SYSTEM_ROLE_IDS.manager]))
    expect(can("pos.report.read")).toBe(true)
  })
})
