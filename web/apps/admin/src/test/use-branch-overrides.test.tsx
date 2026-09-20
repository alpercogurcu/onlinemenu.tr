// Contract tests for the branch-override hooks (ADR-DATA-009). What a
// regression here would NOT show up as a type error: a price-only PUT that
// re-opens a closed product (the backend defaults an omitted is_available to
// true), an optimistic rollback that rewinds a neighbouring row, or a "differs
// on N branches" count inflated by rows that read like "no override".
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import {
  branchOverridesKey,
  useBranchOverrides,
  useDeleteBranchOverride,
  useProductBranchOverrideBranches,
  useUpsertBranchOverride,
} from "@/hooks/use-branch-overrides"
import type { Branch, BranchProductOverride } from "@/types"

const get = vi.fn()
const put = vi.fn()
const del = vi.fn()

vi.mock("@/lib/api", () => ({
  default: {
    get: (...args: unknown[]) => get(...args),
    put: (...args: unknown[]) => put(...args),
    delete: (...args: unknown[]) => del(...args),
  },
}))

const row = (productId: string, patch: Partial<BranchProductOverride> = {}): BranchProductOverride => ({
  branch_id: "b1",
  product_id: productId,
  is_available: true,
  price_amount: null,
  updated_at: "2026-09-20T10:00:00Z",
  ...patch,
})

const branch = (id: string): Branch => ({ id, tenant_id: "t1", name: id, is_active: true })

function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
  return { client, wrapper }
}

beforeEach(() => {
  get.mockReset()
  put.mockReset()
  del.mockReset()
})

describe("useBranchOverrides", () => {
  it("reads the branch's override list and folds a null body into []", async () => {
    get.mockResolvedValue({ data: null })
    const { wrapper } = setup()
    const { result } = renderHook(() => useBranchOverrides("b1"), { wrapper })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(get).toHaveBeenCalledWith("/api/v1/catalog/branches/b1/product-overrides")
    expect(result.current.data).toEqual([])
  })

  it("stays idle without a branch, or when disabled", () => {
    const { wrapper } = setup()
    renderHook(() => useBranchOverrides(""), { wrapper })
    renderHook(() => useBranchOverrides("b1", false), { wrapper })
    expect(get).not.toHaveBeenCalled()
  })
})

describe("useUpsertBranchOverride", () => {
  it("PUTs both fields and writes the row into the cache before the server answers", async () => {
    let resolvePut: (value: unknown) => void = () => {}
    put.mockReturnValue(new Promise((resolve) => (resolvePut = resolve)))
    const { client, wrapper } = setup()
    client.setQueryData(branchOverridesKey("b1"), [row("p2", { price_amount: 500 })])
    const { result } = renderHook(() => useUpsertBranchOverride("b1"), { wrapper })

    act(() => {
      result.current.mutate({ productId: "p1", is_available: false, price_amount: 12_500 })
    })

    await waitFor(() =>
      expect(client.getQueryData<BranchProductOverride[]>(branchOverridesKey("b1"))).toEqual(
        expect.arrayContaining([expect.objectContaining({ product_id: "p1", is_available: false, price_amount: 12_500 })]),
      ),
    )
    expect(put).toHaveBeenCalledWith("/api/v1/catalog/branches/b1/products/p1/override", {
      is_available: false,
      price_amount: 12_500,
    })

    resolvePut({ data: row("p1", { is_available: false, price_amount: 12_500 }) })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
  })

  it("rolls back only its own row when the request fails", async () => {
    put.mockRejectedValue(new Error("boom"))
    // The refetch after settling must not paper over the rollback under test.
    get.mockResolvedValue({ data: [row("p2", { price_amount: 500 })] })
    const { client, wrapper } = setup()
    const before = [row("p1", { price_amount: 1_000 }), row("p2", { price_amount: 500 })]
    client.setQueryData(branchOverridesKey("b1"), before)
    const { result } = renderHook(() => useUpsertBranchOverride("b1"), { wrapper })

    await act(async () => {
      await result.current.mutateAsync({ productId: "p1", is_available: true, price_amount: 2_000 }).catch(() => {})
    })

    const cached = client.getQueryData<BranchProductOverride[]>(branchOverridesKey("b1")) ?? []
    expect(cached.find((r) => r.product_id === "p1")?.price_amount).toBe(1_000)
    expect(cached.find((r) => r.product_id === "p2")?.price_amount).toBe(500)
  })

  it("removes the optimistic row again when a brand-new override fails", async () => {
    put.mockRejectedValue(new Error("boom"))
    get.mockResolvedValue({ data: [] })
    const { client, wrapper } = setup()
    client.setQueryData(branchOverridesKey("b1"), [])
    const { result } = renderHook(() => useUpsertBranchOverride("b1"), { wrapper })

    await act(async () => {
      await result.current.mutateAsync({ productId: "p9", is_available: false, price_amount: null }).catch(() => {})
    })

    expect(client.getQueryData<BranchProductOverride[]>(branchOverridesKey("b1"))).toEqual([])
  })
})

describe("useDeleteBranchOverride", () => {
  it("DELETEs the row and drops it from the cache optimistically", async () => {
    let resolveDelete: (value: unknown) => void = () => {}
    del.mockReturnValue(new Promise((resolve) => (resolveDelete = resolve)))
    const { client, wrapper } = setup()
    client.setQueryData(branchOverridesKey("b1"), [row("p1", { price_amount: 1_000 }), row("p2", { is_available: false })])
    const { result } = renderHook(() => useDeleteBranchOverride("b1"), { wrapper })

    act(() => {
      result.current.mutate({ productId: "p1" })
    })

    await waitFor(() =>
      expect(client.getQueryData<BranchProductOverride[]>(branchOverridesKey("b1"))?.map((r) => r.product_id)).toEqual([
        "p2",
      ]),
    )
    expect(del).toHaveBeenCalledWith("/api/v1/catalog/branches/b1/products/p1/override")
    resolveDelete({ status: 204 })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
  })

  it("puts the row back when the DELETE fails", async () => {
    del.mockRejectedValue(new Error("boom"))
    get.mockResolvedValue({ data: [row("p1", { price_amount: 1_000 })] })
    const { client, wrapper } = setup()
    client.setQueryData(branchOverridesKey("b1"), [row("p1", { price_amount: 1_000 })])
    const { result } = renderHook(() => useDeleteBranchOverride("b1"), { wrapper })

    await act(async () => {
      await result.current.mutateAsync({ productId: "p1" }).catch(() => {})
    })

    expect(client.getQueryData<BranchProductOverride[]>(branchOverridesKey("b1"))).toHaveLength(1)
  })
})

describe("useProductBranchOverrideBranches", () => {
  const branches = [branch("b1"), branch("b2"), branch("b3")]

  beforeEach(() => {
    get.mockImplementation((url: string) => {
      const byBranch: Record<string, BranchProductOverride[]> = {
        "/api/v1/catalog/branches/b1/product-overrides": [row("p1", { price_amount: 1_000 })],
        // Available + no price reads like "no override" — must not be counted.
        "/api/v1/catalog/branches/b2/product-overrides": [row("p1", { branch_id: "b2" })],
        "/api/v1/catalog/branches/b3/product-overrides": [row("p1", { branch_id: "b3", is_available: false })],
      }
      return Promise.resolve({ data: byBranch[url] ?? [] })
    })
  })

  it("lists the branches where the product is priced or closed, ignoring empty rows", async () => {
    const { wrapper } = setup()
    const { result } = renderHook(() => useProductBranchOverrideBranches("p1", branches, true), { wrapper })

    await waitFor(() => expect(result.current.branchIds).toEqual(["b1", "b3"]))
    expect(get).toHaveBeenCalledTimes(3)
  })

  it("fires nothing while disabled (a branch-scoped role would only collect 403s)", () => {
    const { wrapper } = setup()
    const { result } = renderHook(() => useProductBranchOverrideBranches("p1", branches, false), { wrapper })

    expect(get).not.toHaveBeenCalled()
    expect(result.current.branchIds).toEqual([])
  })
})
