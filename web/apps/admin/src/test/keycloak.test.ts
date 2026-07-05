import { beforeEach, describe, expect, it } from "vitest"

import {
  buildAuthorizeUrl,
  buildLogoutUrl,
  consumePkceParams,
  savePkceParams,
} from "@/lib/keycloak"

describe("buildAuthorizeUrl", () => {
  it("includes the PKCE and OIDC required query params", () => {
    const url = buildAuthorizeUrl({
      redirectUri: "http://localhost:3000/auth/callback",
      state: "state-1",
      nonce: "nonce-1",
      codeChallenge: "challenge-1",
    })
    const parsed = new URL(url)

    expect(parsed.pathname).toBe("/realms/onlinemenu/protocol/openid-connect/auth")
    expect(parsed.searchParams.get("client_id")).toBe("admin-panel")
    expect(parsed.searchParams.get("response_type")).toBe("code")
    expect(parsed.searchParams.get("scope")).toBe("openid")
    expect(parsed.searchParams.get("redirect_uri")).toBe("http://localhost:3000/auth/callback")
    expect(parsed.searchParams.get("state")).toBe("state-1")
    expect(parsed.searchParams.get("nonce")).toBe("nonce-1")
    expect(parsed.searchParams.get("code_challenge")).toBe("challenge-1")
    expect(parsed.searchParams.get("code_challenge_method")).toBe("S256")
  })
})

describe("buildLogoutUrl", () => {
  it("includes client_id and post_logout_redirect_uri", () => {
    const url = buildLogoutUrl("http://localhost:3000/login")
    const parsed = new URL(url)

    expect(parsed.pathname).toBe("/realms/onlinemenu/protocol/openid-connect/logout")
    expect(parsed.searchParams.get("client_id")).toBe("admin-panel")
    expect(parsed.searchParams.get("post_logout_redirect_uri")).toBe("http://localhost:3000/login")
    expect(parsed.searchParams.has("id_token_hint")).toBe(false)
  })

  it("adds id_token_hint when an id_token is supplied", () => {
    const url = buildLogoutUrl("http://localhost:3000/login", "id-token-value")
    const parsed = new URL(url)

    expect(parsed.searchParams.get("id_token_hint")).toBe("id-token-value")
  })
})

describe("PKCE session storage round-trip", () => {
  beforeEach(() => {
    sessionStorage.clear()
  })

  it("returns saved params exactly once, then clears them (single-use)", () => {
    savePkceParams({ verifier: "v", state: "st", nonce: "n" })

    expect(consumePkceParams()).toEqual({ verifier: "v", state: "st", nonce: "n" })
    expect(consumePkceParams()).toBeNull()
  })

  it("returns null when nothing was saved", () => {
    expect(consumePkceParams()).toBeNull()
  })
})
