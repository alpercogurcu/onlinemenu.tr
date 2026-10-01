import { render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { describe, expect, it, vi } from "vitest"

import { ProductSheet } from "@/components/menu/product-sheet"
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

describe("ProductSheet odak davranışı", () => {
  it("sheet açılınca not alanına odaklanmaz (mobilde klavye fırlamasın)", () => {
    // Radix, açılışta ilk odaklanabilir elemana odaklanır; seçeneksiz üründe bu
    // not kutusudur ve telefonda klavyeyi fırlatır. Sheet bunu engellemelidir.
    render(
      <NextIntlClientProvider locale="tr" messages={messages}>
        <ProductSheet product={PRODUCT} onClose={vi.fn()} />
      </NextIntlClientProvider>,
    )

    const note = screen.getByLabelText("Not")
    expect(note).toBeInTheDocument()
    expect(note).not.toHaveFocus()
  })
})
