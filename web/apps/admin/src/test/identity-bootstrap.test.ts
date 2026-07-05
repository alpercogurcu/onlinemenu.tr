import { afterEach, describe, expect, it, vi } from "vitest"

import { fetchContexts, fetchMe, selectMembershipContext } from "@/lib/identity-bootstrap"

describe("identity-bootstrap", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it("fetchContexts unwraps the contexts array and sends the Keycloak bearer", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        contexts: [
          {
            membership_id: "m1",
            tenant_id: "t1",
            tenant_name: "Tenant 1",
            role_id: "r1",
            role_name: "cashier",
          },
        ],
        customer: false,
      }),
    })
    vi.stubGlobal("fetch", fetchMock)

    const contexts = await fetchContexts("kc-access-token")

    expect(contexts).toHaveLength(1)
    expect(contexts[0].membership_id).toBe("m1")

    const [url, init] = fetchMock.mock.calls[0]
    expect(String(url)).toContain("/v1/identity/me/contexts")
    expect((init as RequestInit).headers).toMatchObject({
      Authorization: "Bearer kc-access-token",
    })
  })

  it("selectMembershipContext posts membership_id and returns the CTX token", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ token: "ctx-token" }) })
    vi.stubGlobal("fetch", fetchMock)

    const token = await selectMembershipContext("kc-access-token", "m1")

    expect(token).toBe("ctx-token")
    const [url, init] = fetchMock.mock.calls[0]
    expect(String(url)).toContain("/v1/identity/auth/context")
    expect(JSON.parse((init as RequestInit).body as string)).toEqual({ membership_id: "m1" })
  })

  it("fetchMe uses the CTX bearer", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ id: "p1", email: "a@b.com", full_name: "A B", keycloak_sub: "sub", created_at: "" }),
    })
    vi.stubGlobal("fetch", fetchMock)

    const me = await fetchMe("ctx-token")

    expect(me.id).toBe("p1")
    const [, init] = fetchMock.mock.calls[0]
    expect((init as RequestInit).headers).toMatchObject({ Authorization: "Bearer ctx-token" })
  })

  it("throws when the backend responds with a non-2xx status", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 401 }))
    await expect(fetchMe("bad-token")).rejects.toThrow()
  })
})
