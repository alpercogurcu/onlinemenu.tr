import { describe, expect, it } from "vitest"

import { decodeJwtPayload } from "@/lib/jwt"

function base64UrlEncode(obj: unknown): string {
  return Buffer.from(JSON.stringify(obj)).toString("base64url")
}

describe("decodeJwtPayload", () => {
  it("decodes the payload segment without verifying the signature", () => {
    const header = base64UrlEncode({ alg: "RS256" })
    const payload = base64UrlEncode({ nonce: "abc123", sub: "user-1" })
    const token = `${header}.${payload}.fake-signature`

    expect(decodeJwtPayload(token)).toEqual({ nonce: "abc123", sub: "user-1" })
  })

  it("returns null for a token with the wrong number of segments", () => {
    expect(decodeJwtPayload("not-a-jwt")).toBeNull()
  })

  it("returns null for an unparsable payload segment", () => {
    expect(decodeJwtPayload("header.%%%invalid%%%.signature")).toBeNull()
  })
})
