import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { vi } from "vitest"

import { useMenu } from "@/hooks/use-storefront"

const { get } = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock("@/lib/api", () => ({
  default: { get },
  PUBLIC_API_PREFIX: "/api/public/v1",
}))

function createWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
}

describe("useMenu ordering_enabled", () => {
  it("alanı olmayan eski API yanıtında siparişi AÇIK varsayar", async () => {
    // Backend'in eski sürümü alanı hiç göndermez; yokluk "kapalı" sayılsaydı
    // her eski kurulumda satış dururdu.
    get.mockResolvedValueOnce({ data: { categories: [] } })

    const { result } = renderHook(() => useMenu(), { wrapper: createWrapper() })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toEqual({ categories: [], orderingEnabled: true })
  })

  it("ordering_enabled=false yanıtını olduğu gibi taşır", async () => {
    get.mockResolvedValueOnce({ data: { categories: [], ordering_enabled: false } })

    const { result } = renderHook(() => useMenu(), { wrapper: createWrapper() })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.orderingEnabled).toBe(false)
  })
})
