import { describe, expect, it } from "vitest"

import { backendWsOrigin } from "@/lib/kitchen-ws-origin"

describe("backendWsOrigin", () => {
  it("prefers the server-only API_CORE_ORIGIN over the public URL (prod container path)", () => {
    expect(backendWsOrigin("http://api:8080", "http://localhost:8081")).toBe("ws://api:8080")
  })

  it("falls back to NEXT_PUBLIC_API_CORE_URL when API_CORE_ORIGIN is unset (local `pnpm dev`)", () => {
    expect(backendWsOrigin(undefined, "http://localhost:8081")).toBe("ws://localhost:8081")
  })

  it("falls back to localhost:8081 when neither env var is set", () => {
    expect(backendWsOrigin(undefined, undefined)).toBe("ws://localhost:8081")
  })

  it("converts https to wss and strips a trailing slash", () => {
    expect(backendWsOrigin("https://api.example.com/", undefined)).toBe("wss://api.example.com")
  })
})
