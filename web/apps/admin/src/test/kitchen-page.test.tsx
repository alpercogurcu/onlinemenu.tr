// Component-level coverage the plan asked for (m2 in the final review): the
// pos.order.accept cosmetic gate on a single card, and the scoped `.dark`
// root on the full page — both previously covered only by kds.spec.ts, which
// needs the live stack and does not run in CI. Follows dashboard.test.tsx's
// mocking style: mock every hook the page reads instead of standing up real
// query/stream infrastructure.
import { fireEvent, render, screen } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import type { KitchenOrder } from "@/lib/kitchen-events"
import type { Order } from "@/types"

const useCanMock = vi.fn(() => true)
vi.mock("@/hooks/use-can", () => ({
  useCan: () => useCanMock(),
}))

vi.mock("@/hooks/use-tenant", () => ({
  useBranches: () => ({ data: [{ id: "branch-1", name: "Ana Şube" }], isLoading: false }),
}))

vi.mock("@/hooks/use-kitchen-stream", () => ({
  useKitchenStream: () => ({
    status: "live",
    orders: new Map(),
    newOrderIds: new Set(),
    errorMessage: null,
  }),
}))

vi.mock("@/hooks/use-pos", () => ({
  useOrderDetails: () => new Map(),
  useAcceptOrder: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useAdvanceOrder: () => ({ mutateAsync: vi.fn(), isPending: false }),
}))

vi.mock("@/store/auth-store", () => ({
  useAuthStore: (selector: (state: { tenantId: string; user: { id: string; name: string; email: string } }) => unknown) =>
    selector({ tenantId: "tenant-1", user: { id: "person-1", name: "Test", email: "test@onlinemenu.tr" } }),
}))

// Imported after the mocks above so the page module picks them up.
import KitchenPage, { KitchenOrderCard } from "@/app/(main)/pos/kitchen/page"

function baseOrder(overrides: Partial<KitchenOrder> = {}): KitchenOrder {
  return {
    orderId: "order-1",
    checkId: "check-1",
    tableLabel: "Masa 3",
    source: null,
    status: "pending",
    seq: 1,
    occurredAt: new Date().toISOString(),
    isNew: false,
    ...overrides,
  }
}

function baseDetail(): Order {
  return {
    id: "order-1",
    check_id: "check-1",
    tenant_id: "tenant-1",
    branch_id: "branch-1",
    order_channel: "pos",
    status: "pending",
    note: "",
    items: [{ id: "item-1", product_id: "p1", product_name: "Çay", quantity: 2, unit_price_amount: 500, note: "" }],
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  }
}

describe("KitchenOrderCard", () => {
  it("renders the counter-approval gate and no Kabul Et button when canAccept is false", () => {
    render(
      <KitchenOrderCard
        order={baseOrder()}
        detail={baseDetail()}
        now={Date.now()}
        isNew={false}
        onAdvance={() => {}}
        isMutating={false}
        canAccept={false}
      />,
    )

    expect(screen.getByText("Kasa onayı bekleniyor")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Kabul Et" })).not.toBeInTheDocument()
  })

  it("renders the Kabul Et button when canAccept is true", () => {
    render(
      <KitchenOrderCard
        order={baseOrder()}
        detail={baseDetail()}
        now={Date.now()}
        isNew={false}
        onAdvance={() => {}}
        isMutating={false}
        canAccept={true}
      />,
    )

    expect(screen.getByRole("button", { name: "Kabul Et" })).toBeInTheDocument()
    expect(screen.queryByText("Kasa onayı bekleniyor")).not.toBeInTheDocument()
  })
})

describe("KitchenPage device-dark root", () => {
  // jsdom's localStorage is not reliably available under vitest (opaque
  // origin) — see kds-theme.test.tsx for the same fix.
  let store: Record<string, string>

  beforeEach(() => {
    useCanMock.mockReturnValue(true)
    store = {}
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem: (k: string) => (k in store ? store[k] : null),
        setItem: (k: string, v: string) => {
          store[k] = String(v)
        },
        removeItem: (k: string) => {
          delete store[k]
        },
        clear: () => {
          store = {}
        },
        key: () => null,
        length: 0,
      } satisfies Storage,
    })
  })

  it("applies `dark` to [data-kds-root] only after the device-dark switch is toggled, never to <html>", () => {
    render(<KitchenPage />)

    const root = document.querySelector("[data-kds-root]")
    expect(root).not.toBeNull()
    expect(root).not.toHaveClass("dark")
    expect(document.documentElement).not.toHaveClass("dark")

    fireEvent.click(screen.getByRole("switch", { name: "Bu cihazda koyu mod" }))

    expect(root).toHaveClass("dark")
    expect(document.documentElement).not.toHaveClass("dark")
  })
})
