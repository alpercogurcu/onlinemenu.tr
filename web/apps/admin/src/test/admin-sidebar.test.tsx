// AdminSidebar hides the sections whose backend module the tenant has not
// enabled — or that the API does not mount at all (party/hr/billing today),
// so a click can never land on a 404. lib/modules.ts owns the intersection.
import { render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import AdminSidebar from "@/components/layouts/admin-sidebar"
import { SidebarProvider } from "@/components/ui/sidebar"
import { clearAccessToken, setAccessToken } from "@/lib/api"
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

const tenantModules = vi.hoisted(() => ({ value: undefined as string[] | undefined, isError: false }))
vi.mock("@/hooks/use-tenant", () => ({
  useTenantModules: () => ({
    data: tenantModules.value ? { enabled_modules: tenantModules.value } : undefined,
    isError: tenantModules.isError,
  }),
}))

// The sidebar reads the selected branch's POS settings to hide the KDS link on
// the simple flow; both hooks are stubbed so no QueryClient is needed.
const posSettings = vi.hoisted(() => ({
  data: undefined as { order_flow: "full" | "simple" } | undefined,
  isError: false,
  lastOpts: undefined as { enabled?: boolean } | undefined,
  branchId: "branch-1",
}))
vi.mock("@/hooks/use-selected-branch", () => ({
  useSelectedBranch: () => ({ branchId: posSettings.branchId }),
}))
vi.mock("@/hooks/use-pos-branch-settings", () => ({
  usePosBranchSettings: (_branchId: string, opts?: { enabled?: boolean }) => {
    posSettings.lastOpts = opts
    return { data: posSettings.data, isError: posSettings.isError }
  },
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

beforeEach(() => {
  posSettings.data = undefined
  posSettings.isError = false
  posSettings.lastOpts = undefined
  posSettings.branchId = "branch-1"
})

const MANAGER_ROLE_ID = "00000001-0000-0000-0000-000000000006"

function signInAs(roleId: string) {
  const enc = (obj: unknown) => Buffer.from(JSON.stringify(obj)).toString("base64url")
  setAccessToken(`${enc({ alg: "HS256", typ: "CTX" })}.${enc({ rids: [roleId] })}.sig`)
  useAuthStore.setState({ user: { id: "u1", name: "U", email: "u@x" }, tenantId: "tenant-1" })
}

// Module filtering is exercised as the manager: every item is permitted to
// that role, so what disappears here is due to the module intersection alone.
describe("AdminSidebar", () => {
  beforeEach(() => {
    signInAs(MANAGER_ROLE_ID)
    tenantModules.value = undefined
    tenantModules.isError = false
  })

  afterEach(() => {
    clearAccessToken()
    useAuthStore.setState({ user: null, tenantId: null })
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

  it("shows all mounted sections when the modules endpoint errors (e.g. 403)", () => {
    tenantModules.isError = true
    render(<AdminSidebar />, { wrapper: Wrapper })

    expect(groupLabels()).toEqual(["Genel", "POS", "Katalog", "Stok", "Ödeme", "İşletme"])
  })
})

// ADR-DATA-009: "Şube Fiyatları" sits in the Katalog group but only the
// manager (tenant owner) may open it, so every other role must not see a link
// that leads to an access-denied page.
describe("AdminSidebar branch pricing item", () => {
  const ROLE = {
    manager: "00000001-0000-0000-0000-000000000006",
    cashier: "00000001-0000-0000-0000-000000000001",
  }

  function signIn(roleId: string) {
    const enc = (obj: unknown) => Buffer.from(JSON.stringify(obj)).toString("base64url")
    setAccessToken(`${enc({ alg: "HS256", typ: "CTX" })}.${enc({ rids: [roleId] })}.sig`)
    useAuthStore.setState({ user: { id: "u1", name: "U", email: "u@x" }, tenantId: "tenant-1" })
  }

  beforeEach(() => {
    tenantModules.value = undefined
    tenantModules.isError = false
  })

  afterEach(() => {
    clearAccessToken()
    useAuthStore.setState({ user: null, tenantId: null })
  })

  it("shows it to the manager, linking to the pricing screen", () => {
    signIn(ROLE.manager)
    render(<AdminSidebar />, { wrapper: Wrapper })

    expect(screen.getByRole("link", { name: "Şube Fiyatları" })).toHaveAttribute("href", "/catalog/branch-pricing")
  })

  it("hides it from any other role; a cashier sees Genel + POS only", () => {
    signIn(ROLE.cashier)
    render(<AdminSidebar />, { wrapper: Wrapper })

    expect(screen.queryByText("Şube Fiyatları")).not.toBeInTheDocument()
    // 2026-10-01: pos.report.read kasiyere açıldı → Genel Bakış grubu da
    // görünür; Şube Fiyatları ve diğer yönetim grupları hâlâ kapalı.
    expect(groupLabels()).toEqual(["Genel", "POS"])
  })

  it("renders no section at all when nobody is signed in (fail closed)", () => {
    render(<AdminSidebar />, { wrapper: Wrapper })
    expect(screen.queryByText("Şube Fiyatları")).not.toBeInTheDocument()
    expect(groupLabels()).toEqual([])
  })
})

// Section F of the cash/report programme: the simple order flow has no
// kitchen display, so its menu link is hidden too. Fail-open: when the
// setting is unknown the link stays.
describe("AdminSidebar kitchen link (order_flow)", () => {
  const ROLE = {
    manager: "00000001-0000-0000-0000-000000000006",
    cashier: "00000001-0000-0000-0000-000000000001",
    kitchen: "00000001-0000-0000-0000-000000000004",
  }

  function signIn(roleId: string) {
    const enc = (obj: unknown) => Buffer.from(JSON.stringify(obj)).toString("base64url")
    setAccessToken(`${enc({ alg: "HS256", typ: "CTX" })}.${enc({ rids: [roleId] })}.sig`)
    useAuthStore.setState({ user: { id: "u1", name: "U", email: "u@x" }, tenantId: "tenant-1" })
  }

  const kitchenLink = () => screen.queryByRole("link", { name: "Mutfak Ekranı" })

  beforeEach(() => {
    tenantModules.value = undefined
    tenantModules.isError = false
  })

  afterEach(() => {
    clearAccessToken()
    useAuthStore.setState({ user: null, tenantId: null })
  })

  it("hides it for the manager when the selected branch is simple", () => {
    signIn(ROLE.manager)
    posSettings.data = { order_flow: "simple" }
    render(<AdminSidebar />, { wrapper: Wrapper })

    expect(kitchenLink()).not.toBeInTheDocument()
    expect(screen.getByRole("link", { name: "Adisyonlar" })).toBeInTheDocument()
  })

  it("hides it for the cashier on a simple branch", () => {
    signIn(ROLE.cashier)
    posSettings.data = { order_flow: "simple" }
    render(<AdminSidebar />, { wrapper: Wrapper })

    expect(kitchenLink()).not.toBeInTheDocument()
  })

  it("keeps it on the full flow", () => {
    signIn(ROLE.manager)
    posSettings.data = { order_flow: "full" }
    render(<AdminSidebar />, { wrapper: Wrapper })

    expect(kitchenLink()).toBeInTheDocument()
  })

  it("keeps it while the settings are loading, on error, or with no branch (fail-open)", () => {
    signIn(ROLE.manager)
    const { unmount } = render(<AdminSidebar />, { wrapper: Wrapper })
    expect(kitchenLink()).toBeInTheDocument()
    unmount()

    posSettings.isError = true
    const second = render(<AdminSidebar />, { wrapper: Wrapper })
    expect(kitchenLink()).toBeInTheDocument()
    second.unmount()

    posSettings.branchId = ""
    render(<AdminSidebar />, { wrapper: Wrapper })
    expect(kitchenLink()).toBeInTheDocument()
  })

  it("does not fetch settings for the kitchen role (no pos.check.read) and keeps its menu", () => {
    signIn(ROLE.kitchen)
    render(<AdminSidebar />, { wrapper: Wrapper })

    expect(posSettings.lastOpts?.enabled).toBe(false)
    expect(kitchenLink()).toBeInTheDocument()
  })
})
