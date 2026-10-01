import { fireEvent, render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { CategoryNav } from "@/components/menu/category-nav"
import messages from "@/messages/tr.json"

function renderNav(ui: React.ReactElement) {
  return render(
    <NextIntlClientProvider locale="tr" messages={messages}>
      {ui}
    </NextIntlClientProvider>,
  )
}

const CATEGORIES = [
  { id: "cat-1", title: "Burgerler" },
  { id: "cat-2", title: "İçecekler" },
]

describe("CategoryNav", () => {
  beforeEach(() => {
    // jsdom'da IntersectionObserver yok; bileşen gözlemciyi yalnız varsa kurar.
    vi.unstubAllGlobals()
  })

  it("her kategori için bir çip basar ve nav erişilebilir ada sahiptir", () => {
    renderNav(<CategoryNav categories={CATEGORIES} />)
    expect(screen.getByRole("navigation", { name: "Kategoriye git" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Burgerler" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "İçecekler" })).toBeInTheDocument()
  })

  it("çipe dokununca ilgili bölüme kayar", () => {
    const section = document.createElement("section")
    section.id = "category-section-cat-2"
    document.body.appendChild(section)
    const scrollIntoView = vi.fn()
    section.scrollIntoView = scrollIntoView

    renderNav(<CategoryNav categories={CATEGORIES} />)
    fireEvent.click(screen.getByRole("button", { name: "İçecekler" }))

    expect(scrollIntoView).toHaveBeenCalledTimes(1)
    section.remove()
  })

  it("ilk kategori başlangıçta geçerli bölüm olarak işaretlidir", () => {
    renderNav(<CategoryNav categories={CATEGORIES} />)
    expect(screen.getByRole("button", { name: "Burgerler" })).toHaveAttribute("aria-current", "true")
    expect(screen.getByRole("button", { name: "İçecekler" })).not.toHaveAttribute("aria-current")
  })
})
