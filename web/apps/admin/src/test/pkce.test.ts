import { describe, expect, it } from "vitest"

import {
  generateCodeChallenge,
  generateCodeVerifier,
  generateNonce,
  generateState,
} from "@/lib/pkce"

describe("generateCodeChallenge", () => {
  it("matches the RFC 7636 Appendix B.1 S256 test vector", async () => {
    const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
    const challenge = await generateCodeChallenge(verifier)
    expect(challenge).toBe("E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM")
  })

  it("is deterministic for a given verifier", async () => {
    const verifier = generateCodeVerifier()
    const a = await generateCodeChallenge(verifier)
    const b = await generateCodeChallenge(verifier)
    expect(a).toBe(b)
  })
})

describe("generateCodeVerifier", () => {
  it("produces a 43-character unreserved base64url string (RFC 7636 §4.1)", () => {
    const verifier = generateCodeVerifier()
    expect(verifier).toHaveLength(43)
    expect(verifier).toMatch(/^[A-Za-z0-9\-_]+$/)
  })

  it("produces distinct verifiers across calls", () => {
    const a = generateCodeVerifier()
    const b = generateCodeVerifier()
    expect(a).not.toBe(b)
  })
})

describe("generateState / generateNonce", () => {
  it("produce URL-safe, non-empty, mutually distinct tokens", () => {
    const state = generateState()
    const nonce = generateNonce()
    expect(state).not.toBe(nonce)
    expect(state.length).toBeGreaterThan(0)
    expect(nonce.length).toBeGreaterThan(0)
    expect(state).toMatch(/^[A-Za-z0-9\-_]+$/)
    expect(nonce).toMatch(/^[A-Za-z0-9\-_]+$/)
  })

  it("produce distinct values across calls (CSRF/replay protection depends on this)", () => {
    expect(generateState()).not.toBe(generateState())
    expect(generateNonce()).not.toBe(generateNonce())
  })
})
