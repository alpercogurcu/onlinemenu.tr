// Contract tests for the two new use-catalog hooks whose regressions would
// not show up as a type error: useProductsModifierGroupIds must fire one
// request per product (a naive implementation could join them into a single
// request the backend does not support, or refetch on every render), and
// useAssignModifierGroup must invalidate BOTH sides of the product<->group
// relationship — a product's detail page and the group's own product list —
// or one of the two screens goes stale after a reassignment.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { useAssignModifierGroup, useGroupProductIds, useProductsModifierGroupIds } from "@/hooks/use-catalog"

const get = vi.fn()
const post = vi.fn()

vi.mock("@/lib/api", () => ({
  default: {
    get: (...args: unknown[]) => get(...args),
    post: (...args: unknown[]) => post(...args),
  },
}))

function wrapperWithClient(client: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
}

describe("useProductsModifierGroupIds", () => {
  beforeEach(() => {
    get.mockReset()
    get.mockImplementation((url: string) => {
      const groups: Record<string, string[]> = {
        "/api/v1/catalog/products/p1/modifier-groups": ["g1", "g2"],
        "/api/v1/catalog/products/p2/modifier-groups": [],
        "/api/v1/catalog/products/p3/modifier-groups": ["g3"],
      }
      return Promise.resolve({ data: groups[url] ?? [] })
    })
  })

  it("fires one request per product and maps ids back to their product", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { result } = renderHook(() => useProductsModifierGroupIds(["p1", "p2", "p3"]), {
      wrapper: wrapperWithClient(client),
    })

    await waitFor(() => expect(result.current.p1).toEqual(["g1", "g2"]))

    expect(get).toHaveBeenCalledTimes(3)
    expect(get).toHaveBeenCalledWith("/api/v1/catalog/products/p1/modifier-groups")
    expect(get).toHaveBeenCalledWith("/api/v1/catalog/products/p2/modifier-groups")
    expect(get).toHaveBeenCalledWith("/api/v1/catalog/products/p3/modifier-groups")
    expect(result.current).toEqual({ p1: ["g1", "g2"], p2: [], p3: ["g3"] })
  })

  it("returns an empty array for a product whose query has not resolved yet", () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { result } = renderHook(() => useProductsModifierGroupIds(["p1"]), {
      wrapper: wrapperWithClient(client),
    })

    expect(result.current.p1).toEqual([])
  })

  // The id-list endpoints (GET /products/{id}/modifier-groups,
  // GET /modifier-groups/{id}/products, GET /modifier-groups/{gid}/modifiers)
  // serialize an empty result as JSON null, not []. Every list hook must
  // fold that into [] rather than handing a table row `.map` over `null`.
  //
  // useProductsModifierGroupIds folds straight to a plain map, so
  // result.current.p1 reads [] BOTH before the query has resolved (see the
  // "not resolved yet" test above) and after a folded-null success — {p1: []}
  // alone can't tell those apart. Hold the response open with a manual
  // promise (same technique as confirm-dialog.test.tsx's resolveConfirm) and
  // assert the query is still pending beforehand, then — mirroring how the
  // useGroupProductIds test below waits on the query settling rather than on
  // the hook's default — wait on the underlying query state reaching
  // "success" before asserting the fold.
  it("folds a null response body into an empty array, not null", async () => {
    get.mockReset()
    let resolveGet!: (value: { data: null }) => void
    get.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveGet = resolve
        }),
    )
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    const { result } = renderHook(() => useProductsModifierGroupIds(["p1"]), {
      wrapper: wrapperWithClient(client),
    })

    await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
    expect(client.getQueryState(["product-modifier-groups", "p1"])?.status).toBe("pending")

    resolveGet({ data: null })

    await waitFor(() =>
      expect(client.getQueryState(["product-modifier-groups", "p1"])?.status).toBe("success"),
    )
    expect(result.current).toEqual({ p1: [] })
  })
})

describe("useGroupProductIds", () => {
  it("folds a null response body into an empty array, not null", async () => {
    get.mockReset()
    get.mockResolvedValue({ data: null })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    const { result } = renderHook(() => useGroupProductIds("g1"), { wrapper: wrapperWithClient(client) })

    await waitFor(() => expect(result.current.data).toEqual([]))
    expect(get).toHaveBeenCalledWith("/api/v1/catalog/modifier-groups/g1/products")
  })
})

describe("useAssignModifierGroup", () => {
  beforeEach(() => {
    post.mockReset()
    post.mockResolvedValue({ data: undefined })
  })

  it("invalidates both the product's and the group's cached ids on success", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const invalidateSpy = vi.spyOn(client, "invalidateQueries")

    const { result } = renderHook(() => useAssignModifierGroup(), { wrapper: wrapperWithClient(client) })

    result.current.mutate({ productId: "p1", groupId: "g1", sortOrder: 10 })

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith("/api/v1/catalog/products/p1/modifier-groups", {
      group_id: "g1",
      sort_order: 10,
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["product-modifier-groups", "p1"] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["group-products", "g1"] })
  })
})
