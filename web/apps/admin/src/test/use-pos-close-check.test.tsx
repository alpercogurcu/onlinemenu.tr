// useCloseCheck once POSTed without Idempotency-Key, so the idempotency
// middleware answered 422 to every press of the admin "Kapat" button.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook } from "@testing-library/react"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { useCloseCheck } from "@/hooks/use-pos"

const post = vi.fn()

vi.mock("@/lib/api", () => ({
  default: {
    post: (...args: unknown[]) => post(...args),
  },
}))

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

function idempotencyKeyOf(call: unknown[]): string | undefined {
  const config = call[2] as { headers?: Record<string, string> } | undefined
  return config?.headers?.["Idempotency-Key"]
}

describe("useCloseCheck", () => {
  beforeEach(() => {
    post.mockReset()
    post.mockResolvedValue({ data: {} })
  })

  it("sends an Idempotency-Key with the close request", async () => {
    const { result } = renderHook(() => useCloseCheck(), { wrapper })

    await result.current.mutateAsync("c1")

    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][0]).toBe("/api/v1/pos/checks/c1/close")
    expect(idempotencyKeyOf(post.mock.calls[0])).toMatch(UUID)
  })

  it("mints a fresh key for each close attempt", async () => {
    const { result } = renderHook(() => useCloseCheck(), { wrapper })

    await result.current.mutateAsync("c1")
    await result.current.mutateAsync("c2")

    const first = idempotencyKeyOf(post.mock.calls[0])
    const second = idempotencyKeyOf(post.mock.calls[1])
    expect(first).toMatch(UUID)
    expect(second).toMatch(UUID)
    expect(first).not.toBe(second)
  })
})
