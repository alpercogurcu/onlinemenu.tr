// AdminSidebar hides the sections whose backend module the tenant has not
// enabled — or that the API does not mount at all (party/hr/billing today),
// so a click can never land on a 404. lib/modules.ts owns the intersection.
import { render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import AdminSidebar from "@/components/layouts/admin-sidebar"
import { SidebarProvider } from "@/components/ui/sidebar"
import { MOUNTED_MODULES, resolveEnabledModules } from "@/lib/modules"
import messages from "@/messages/tr.json"
import { useAuthStore } from "@/store/auth-store"

vi.mock("next/navigation", () => ({
  usePathname: () => "/",
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
}))

vi.mock("next-themes", () => ({
  useTheme: () => ({ theme: "light", setTheme: vi.fn() }),
}))

vi.mock("@/hooks/use-identity", () => ({
  useMe: () => ({ data: { full_name: "Test Admin", email: "admin@test" }, isLoading: false }),
}))

const tenantModules = vi.hoisted(() => ({ value: undefined as string[] | undefined }))
vi.mock("@/hooks/use-tenant", () => ({
  useTenant: () => ({ data: tenantModules.value ? { enabled_modules: tenantModules.value } : undefined }),
}))

// shadcn's SidebarProvider reads window.matchMedia through useIsMobile; jsdom
// has no implementation.
vi.stubGlobal(
  "matchMedia",
  vi.fn().mockImplementation(() => ({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })),
)

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <NextIntlClientProvider locale="tr" messages={messages}>
      <SidebarProvider>{children}</SidebarProvider>
    </NextIntlClientProvider>
  )
}

const UNMOUNTED = ["Müşteriler", "Fatura", "Personel"]

// Group labels only — "Genel" is also a settings menu item, so a bare text
// query would be ambiguous.
function groupLabels(): string[] {
  return Array.from(document.querySelectorAll('[data-sidebar="group-label"]')).map(
    (el) => el.textContent ?? "",
  )
}

describe("resolveEnabledModules", () => {
  it("shows every mounted module before the tenant has loaded", () => {
    expect(resolveEnabledModules(undefined)).toEqual([...MOUNTED_MODULES])
  })

  it("intersects the tenant's modules with the mounted ones", () => {
    expect(resolveEnabledModules(["pos", "party", "hr", "billing"])).toEqual(["pos"])
    expect(resolveEnabledModules([])).toEqual([])
  })
})

describe("AdminSidebar", () => {
  beforeEach(() => {
    useAuthStore.setState({ tenantId: "tenant-1" })
    tenantModules.value = undefined
  })

  it("never renders the sections whose module the API does not mount", () => {
    tenantModules.value = ["pos", "catalog", "inventory", "party", "billing", "hr"]
    render(<AdminSidebar />, { wrapper: Wrapper })

    expect(groupLabels()).toEqual(["Genel", "POS", "Katalog", "Stok", "Ödeme", "İşletme"])
    for (const label of UNMOUNTED) {
      expect(screen.queryByText(label)).not.toBeInTheDocument()
    }
  })

  it("drops a mounted section the tenant has not enabled", () => {
    tenantModules.value = ["pos", "catalog"]
    render(<AdminSidebar />, { wrapper: Wrapper })

    expect(groupLabels()).toEqual(["Genel", "POS", "Katalog", "Ödeme", "İşletme"])
    expect(screen.queryByText("Stok")).not.toBeInTheDocument()
  })

  it("hides the payment section together with pos", () => {
    tenantModules.value = ["catalog", "inventory"]
    render(<AdminSidebar />, { wrapper: Wrapper })

    expect(groupLabels()).toEqual(["Genel", "Katalog", "Stok", "İşletme"])
  })

  it("shows all mounted sections while the tenant is still loading", () => {
    render(<AdminSidebar />, { wrapper: Wrapper })

    expect(groupLabels()).toEqual(["Genel", "POS", "Katalog", "Stok", "Ödeme", "İşletme"])
  })
})
