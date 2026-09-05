// Contract test for the KDS batch order fetch. What can silently go wrong
// here is not correctness of the data but the number of requests: the hook
// exists solely to turn one-XHR-per-ticket into one batch, and a regression
// (re-keying the query on the full id list, or forgetting the "already
// resolved" bookkeeping) still renders a perfectly correct board while
// firing hundreds of requests again. So every assertion below is about the
// calls, not just the result.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { useOrderDetails } from "@/hooks/use-pos"
import type { Order } from "@/types"

const get = vi.fn()

vi.mock("@/lib/api", () => ({
  default: {
    get: (...args: unknown[]) => get(...args),
  },
}))

function orderOf(id: string): Order {
  return {
    id,
    check_id: null,
    tenant_id: "aaaaaaaa-0000-0000-0000-000000000001",
    branch_id: "cccccccc-0000-0000-0000-000000000001",
    order_channel: "dine_in",
    status: "pending",
    note: "",
    items: [{ id: `${id}-item`, product_id: "p1", product_name: "Adana", quantity: 1, unit_price_amount: 16000, note: "" }],
    created_at: "2026-08-06T10:00:00Z",
    updated_at: "2026-08-06T10:00:00Z",
  }
}

// Mirrors the real endpoint: returns an order per requested id, and simply
// omits ids it does not know (the partial-result contract).
function respondWithKnownIds(known: (id: string) => boolean) {
  get.mockImplementation((_url: string, config: { params: { ids: string } }) => {
    const ids = config.params.ids.split(",")
    return Promise.resolve({ data: ids.filter(known).map(orderOf) })
  })
}

function requestedIds(): string[][] {
  return get.mock.calls.map(([, config]) => (config as { params: { ids: string } }).params.ids.split(","))
}

function Wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

function ids(count: number, prefix = "o"): string[] {
  return Array.from({ length: count }, (_, i) => `${prefix}${i}`)
}

describe("useOrderDetails", () => {
  beforeEach(() => {
    get.mockReset()
    respondWithKnownIds(() => true)
  })

  it("resolves many orders with a single request", async () => {
    const { result } = renderHook(() => useOrderDetails(ids(50)), { wrapper: Wrapper })

    await waitFor(() => expect(result.current.size).toBe(50))
    expect(get).toHaveBeenCalledTimes(1)
    expect(get).toHaveBeenCalledWith("/api/v1/pos/orders", { params: { ids: ids(50).join(",") } })
    expect(result.current.get("o7")?.items).toHaveLength(1)
  })

  it("splits an oversized board across requests, none exceeding the batch size", async () => {
    const { result } = renderHook(() => useOrderDetails(ids(250)), { wrapper: Wrapper })

    await waitFor(() => expect(result.current.size).toBe(250))
    const batches = requestedIds()
    expect(batches).toHaveLength(3)
    for (const batch of batches) expect(batch.length).toBeLessThanOrEqual(100)
    expect(batches.flat()).toEqual(ids(250))
  })

  it("fetches only the newly arrived order, not the whole board again", async () => {
    const { result, rerender } = renderHook(({ list }: { list: string[] }) => useOrderDetails(list), {
      wrapper: Wrapper,
      initialProps: { list: ids(30) },
    })
    await waitFor(() => expect(result.current.size).toBe(30))
    expect(get).toHaveBeenCalledTimes(1)

    rerender({ list: [...ids(30), "fresh"] })

    await waitFor(() => expect(result.current.size).toBe(31))
    expect(get).toHaveBeenCalledTimes(2)
    expect(requestedIds()[1]).toEqual(["fresh"])
  })

  it("does not re-request an id the backend never returned", async () => {
    respondWithKnownIds((id) => id !== "ghost")
    const { result, rerender } = renderHook(({ list }: { list: string[] }) => useOrderDetails(list), {
      wrapper: Wrapper,
      initialProps: { list: ["o0", "ghost"] },
    })
    await waitFor(() => expect(result.current.size).toBe(1))

    rerender({ list: ["o0", "ghost", "o1"] })

    await waitFor(() => expect(result.current.size).toBe(2))
    expect(requestedIds()[1]).toEqual(["o1"])
  })

  it("drops orders that left the board", async () => {
    const { result, rerender } = renderHook(({ list }: { list: string[] }) => useOrderDetails(list), {
      wrapper: Wrapper,
      initialProps: { list: ["o0", "o1"] },
    })
    await waitFor(() => expect(result.current.size).toBe(2))

    rerender({ list: ["o1"] })

    await waitFor(() => expect(result.current.size).toBe(1))
    expect(result.current.has("o0")).toBe(false)
  })

  it("makes no request when the board is empty", () => {
    const { result } = renderHook(() => useOrderDetails([]), { wrapper: Wrapper })

    expect(result.current.size).toBe(0)
    expect(get).not.toHaveBeenCalled()
  })

  // A ticket moving between KDS columns reorders `allOrderIds` (a flatMap
  // over columns) without changing the set of ids on the board. That must
  // not look like a new id list and re-request everything already resolved.
  it("reordering the same ids makes no new request", async () => {
    const { result, rerender } = renderHook(({ list }: { list: string[] }) => useOrderDetails(list), {
      wrapper: Wrapper,
      initialProps: { list: ids(3) },
    })
    await waitFor(() => expect(result.current.size).toBe(3))
    expect(get).toHaveBeenCalledTimes(1)

    rerender({ list: [...ids(3)].reverse() })

    expect(result.current.size).toBe(3)
    expect(get).toHaveBeenCalledTimes(1)
  })

  // A fetch started for the board as it was a moment ago must still land its
  // result once it settles, even though the id list has since changed and
  // the effect that started it has already been superseded.
  it("a fetch in flight when the list changes still resolves its orders", async () => {
    let resolveFirst!: (value: { data: Order[] }) => void
    const first = new Promise<{ data: Order[] }>((resolve) => {
      resolveFirst = resolve
    })
    get.mockImplementationOnce(() => first)

    const { result, rerender } = renderHook(({ list }: { list: string[] }) => useOrderDetails(list), {
      wrapper: Wrapper,
      initialProps: { list: ids(3) },
    })
    expect(get).toHaveBeenCalledTimes(1)

    rerender({ list: [...ids(3), "fresh"] })
    await waitFor(() => expect(result.current.has("fresh")).toBe(true))
    expect(get).toHaveBeenCalledTimes(2)

    resolveFirst({ data: ids(3).map(orderOf) })

    await waitFor(() => expect(result.current.size).toBe(4))
    for (const id of ids(3)) expect(result.current.has(id)).toBe(true)
    expect(get).toHaveBeenCalledTimes(2)
  })
})
