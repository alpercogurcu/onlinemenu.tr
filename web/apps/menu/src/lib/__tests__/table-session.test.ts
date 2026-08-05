import { describe, expect, it } from "vitest"

import { decodeTableSession, encodeTableSession } from "@/lib/table-session"

// This pair is written by a Route Handler and read by the browser, and it
// already produced one real bug: NextResponse.cookies.set() percent-encoded
// the value a SECOND time, so decodeURIComponent returned "%7B%22..." and
// JSON.parse threw. The round trip is now asserted rather than assumed.
describe("table session cookie", () => {
  it("round-trips a label with spaces and Turkish characters", () => {
    const info = { tableLabel: "Bahçe Masa 4", expiresAt: "2026-08-05T22:00:00.000Z" }
    expect(decodeTableSession(encodeTableSession(info))).toEqual(info)
  })

  it("encodes to a value with no cookie-breaking characters", () => {
    const encoded = encodeTableSession({ tableLabel: "Masa; 4=x", expiresAt: "2026-01-01T00:00:00Z" })
    expect(encoded).not.toMatch(/[;,\s"]/)
  })

  it("returns null for a double-encoded value instead of throwing", () => {
    const encoded = encodeTableSession({ tableLabel: "Masa 4", expiresAt: "2026-01-01T00:00:00Z" })
    expect(decodeTableSession(encodeURIComponent(encoded))).toBeNull()
  })

  it("returns null for a truncated cookie", () => {
    expect(decodeTableSession("%7B%22tableLabel%22")).toBeNull()
  })

  it("returns null when the shape is wrong", () => {
    expect(decodeTableSession(encodeURIComponent(JSON.stringify({ tableLabel: 4 })))).toBeNull()
  })
})
