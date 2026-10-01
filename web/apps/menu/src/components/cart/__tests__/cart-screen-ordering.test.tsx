import { render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { CartScreen } from "@/components/cart/cart-screen"
import { useCartStore } from "@/lib/cart-store"
import messages from "@/messages/tr.json"
import type { MenuProduct } from "@/types/storefront"

const PRODUCT: MenuProduct = {
  id: "p1",
  name: "American Smash Burger",
  description: "",
  price_amount: 49_000,
  currency: "TRY",
  image_key: "",
  allergens: [],
  is_available: true,
  modifier_groups: [],
}

const { menuState, placeOrderState } = vi.hoisted(() => ({
  menuState: {
    data: undefined as { categories: unknown[]; orderingEnabled: boolean } | undefined,
  },
  placeOrderState: { mutate: vi.fn(), isPending: false },
}))

vi.mock("@/hooks/use-storefront", () => ({
  useMenu: () => menuState,
  usePlaceOrder: () => placeOrderState,
}))

vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: vi.fn(), push: vi.fn() }),
}))

function renderCart() {
  return render(
    <NextIntlClientProvider locale="tr" messages={messages}>
      <CartScreen />
    </NextIntlClientProvider>,
  )
}

describe("sipariş kapalıyken sepet ekranı", () => {
  beforeEach(() => {
    useCartStore.setState({ lines: [], orderNote: "", idempotencyKey: null, sessionKey: null })
    useCartStore.getState().addLine(PRODUCT, [], "", 1)
  })

  it("'Siparişi gönder' disabled olur ve bilgi şeridi basılır", () => {
    menuState.data = { categories: [], orderingEnabled: false }

    renderCart()

    expect(screen.getByRole("button", { name: messages.cart.submit })).toBeDisabled()
    expect(screen.getByText(messages.menu.orderingClosedTitle)).toBeInTheDocument()
  })

  it("açıkken gönderim serbesttir, şerit yoktur", () => {
    menuState.data = { categories: [], orderingEnabled: true }

    renderCart()

    expect(screen.getByRole("button", { name: messages.cart.submit })).toBeEnabled()
    expect(screen.queryByText(messages.menu.orderingClosedTitle)).not.toBeInTheDocument()
  })

  it("menü yanıtı henüz yokken gönderim kapatılmaz (sunucu 409'u yetkilidir)", () => {
    menuState.data = undefined

    renderCart()

    expect(screen.getByRole("button", { name: messages.cart.submit })).toBeEnabled()
  })
})
