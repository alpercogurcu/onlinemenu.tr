import { render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { MenuScreen } from "@/components/menu/menu-screen"
import { ProductSheet } from "@/components/menu/product-sheet"
import { useCartStore } from "@/lib/cart-store"
import messages from "@/messages/tr.json"
import type { MenuCategory, MenuProduct } from "@/types/storefront"

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

const CATEGORIES: MenuCategory[] = [
  { id: "cat-1", name: "Burgerler", sort_order: 1, products: [PRODUCT] },
]

// useMenu tüm ekranın tek veri kaynağı; react-query/ağ yerine hook mock'lanır
// ki test yalnız "kapalıyken ne görünür" sorusuna cevap versin. vi.hoisted
// içinde yalnız tip referansı var — değer referansı TDZ hatası verirdi.
const { menuState } = vi.hoisted(() => ({
  menuState: {
    data: undefined as { categories: MenuCategory[]; orderingEnabled: boolean } | undefined,
    isPending: false,
    isError: false,
    error: null as unknown,
    refetch: () => Promise.resolve(),
  },
}))

vi.mock("@/hooks/use-storefront", () => ({
  useMenu: () => menuState,
}))

function renderWithIntl(ui: React.ReactElement) {
  return render(
    <NextIntlClientProvider locale="tr" messages={messages}>
      {ui}
    </NextIntlClientProvider>,
  )
}

const CLOSED_TITLE = messages.menu.orderingClosedTitle

describe("sipariş kapalıyken menü ekranı", () => {
  beforeEach(() => {
    menuState.data = { categories: CATEGORIES, orderingEnabled: true }
    useCartStore.setState({ lines: [], orderNote: "", idempotencyKey: null, sessionKey: null })
  })

  it("kapalıyken bilgi şeridi basılır ve dolu sepete rağmen CartBar görünmez", () => {
    menuState.data = { categories: CATEGORIES, orderingEnabled: false }
    useCartStore.getState().addLine(PRODUCT, [], "", 2)

    renderWithIntl(<MenuScreen />)

    expect(screen.getByText(CLOSED_TITLE)).toBeInTheDocument()
    expect(screen.getByText(messages.menu.orderingClosedHint)).toBeInTheDocument()
    expect(screen.queryByRole("link", { name: /ürün/ })).not.toBeInTheDocument()
  })

  it("açıkken şerit basılmaz ve dolu sepette CartBar görünür", () => {
    useCartStore.getState().addLine(PRODUCT, [], "", 2)

    renderWithIntl(<MenuScreen />)

    expect(screen.queryByText(CLOSED_TITLE)).not.toBeInTheDocument()
    expect(screen.getByRole("link", { name: /2 ürün/ })).toBeInTheDocument()
  })
})

describe("sipariş kapalıyken ProductSheet", () => {
  it("ürün detayı açılır ama 'Sepete ekle' yerine kapalı notu gösterilir", () => {
    renderWithIntl(<ProductSheet product={PRODUCT} orderingEnabled={false} onClose={vi.fn()} />)

    expect(screen.getByText(PRODUCT.name)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /Sepete ekle/ })).not.toBeInTheDocument()
    expect(screen.getByText(messages.product.orderingClosed)).toBeInTheDocument()
    // Eklenemeyecek ürüne not yazmak çıkmaz sokak: giriş alanı da gizlenir.
    expect(screen.queryByLabelText(messages.product.note)).not.toBeInTheDocument()
  })

  it("açıkken 'Sepete ekle' durur, kapalı notu basılmaz", () => {
    renderWithIntl(<ProductSheet product={PRODUCT} orderingEnabled onClose={vi.fn()} />)

    expect(screen.getByRole("button", { name: /Sepete ekle/ })).toBeInTheDocument()
    expect(screen.queryByText(messages.product.orderingClosed)).not.toBeInTheDocument()
  })
})
