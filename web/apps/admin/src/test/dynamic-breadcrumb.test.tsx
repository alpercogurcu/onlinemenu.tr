// Light coverage for DynamicBreadcrumb's two additions: Turkish names for a
// "new" entity segment (instead of the raw literal "new" title-cased to
// "New"), and useBreadcrumbLabel letting a detail page swap a UUID segment
// for the entity's own name.
import { render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { describe, expect, it, vi } from "vitest"

import DynamicBreadcrumb, { useBreadcrumbLabel } from "@/components/layouts/dynamic-breadcrumb"
import messages from "@/messages/tr.json"

let pathname = "/catalog/products/new"
vi.mock("next/navigation", () => ({
  usePathname: () => pathname,
}))

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <NextIntlClientProvider locale="tr" messages={messages}>
      {children}
    </NextIntlClientProvider>
  )
}

describe("DynamicBreadcrumb", () => {
  it('renders "Yeni Ürün" for /catalog/products/new', () => {
    pathname = "/catalog/products/new"
    render(<DynamicBreadcrumb />, { wrapper: Wrapper })

    expect(screen.getByText("Yeni Ürün")).toBeInTheDocument()
  })

  it('renders "Yeni Grup" for /catalog/modifiers/new', () => {
    pathname = "/catalog/modifiers/new"
    render(<DynamicBreadcrumb />, { wrapper: Wrapper })

    expect(screen.getByText("Yeni Grup")).toBeInTheDocument()
  })

  it("uses the label registered via useBreadcrumbLabel for a UUID segment", () => {
    pathname = "/catalog/products/prod-1"

    function ProductPageStub() {
      useBreadcrumbLabel("Adana Kebap")
      return <DynamicBreadcrumb />
    }

    render(<ProductPageStub />, { wrapper: Wrapper })

    expect(screen.getByText("Adana Kebap")).toBeInTheDocument()
    expect(screen.queryByText("Prod 1")).not.toBeInTheDocument()
  })

  it("falls back to a title-cased segment when no label was registered", () => {
    pathname = "/catalog/products/prod-2"
    render(<DynamicBreadcrumb />, { wrapper: Wrapper })

    expect(screen.getByText("Prod 2")).toBeInTheDocument()
  })

  it("names every inventory and settings segment in Turkish", () => {
    pathname = "/inventory/supply-policies"
    const { unmount } = render(<DynamicBreadcrumb />, { wrapper: Wrapper })
    expect(screen.getByText("Stok")).toBeInTheDocument()
    expect(screen.getByText("Tedarik Politikaları")).toBeInTheDocument()
    unmount()

    pathname = "/settings/fiscal-sections"
    render(<DynamicBreadcrumb />, { wrapper: Wrapper })
    expect(screen.getByText("İşletme")).toBeInTheDocument()
    expect(screen.getByText("Kısım Eşleme")).toBeInTheDocument()
    expect(screen.queryByText("Fiscal Sections")).not.toBeInTheDocument()
  })

  it("labels /billing/settings as the provider settings, not the business section", () => {
    pathname = "/billing/settings"
    render(<DynamicBreadcrumb />, { wrapper: Wrapper })

    expect(screen.getByText("Fatura")).toBeInTheDocument()
    expect(screen.getByText("Sağlayıcı Ayarları")).toBeInTheDocument()
    expect(screen.queryByText("İşletme")).not.toBeInTheDocument()
  })

  it('renders "Detay" for an unlabeled UUID segment', () => {
    pathname = "/catalog/products/0b0d3f4e-8e0a-4c1e-9b4a-2f1c6d7e8a9b"
    render(<DynamicBreadcrumb />, { wrapper: Wrapper })

    expect(screen.getByText("Detay")).toBeInTheDocument()
  })
})
