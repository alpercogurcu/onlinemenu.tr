// Users page: names/e-mails instead of UUIDs (server-joined person_name /
// person_email / role_name), search + role/branch filters, 25-row client
// pagination, and the status action that hits PUT /memberships/{id}.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import UsersPage, { PAGE_SIZE, filterMembers, initials } from "@/app/(main)/settings/users/page"
import { useAuthStore } from "@/store/auth-store"
import type { Membership } from "@/types"

const get = vi.fn()
const put = vi.fn()

vi.mock("@/lib/api", () => ({
  default: {
    get: (...args: unknown[]) => get(...args),
    post: vi.fn(),
    put: (...args: unknown[]) => put(...args),
  },
  getAccessToken: () => null,
  setAccessToken: vi.fn(),
  clearAccessToken: vi.fn(),
}))

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const TENANT = "tenant-1"
const ROLE_MANAGER = { id: "role-manager", name: "Yönetici", branch_scoped: false }
const ROLE_CASHIER = { id: "role-cashier", name: "Kasiyer", branch_scoped: true }
const BRANCH = { id: "branch-1", name: "Ana Şube" }

function member(i: number, overrides: Partial<Membership> = {}): Membership {
  return {
    id: `m-${i}`,
    person_id: `p-${i}`,
    person_name: `Kişi ${i}`,
    person_email: `kisi${i}@ornek.com`,
    tenant_id: TENANT,
    branch_id: BRANCH.id,
    role_id: ROLE_CASHIER.id,
    role_name: ROLE_CASHIER.name,
    status: "active",
    ...overrides,
  }
}

function mockApi(members: Membership[]) {
  get.mockImplementation(async (url: string) => {
    if (url.endsWith("/memberships")) return { data: { memberships: members } }
    if (url.endsWith("/roles")) return { data: { roles: [ROLE_MANAGER, ROLE_CASHIER] } }
    if (url.endsWith("/branches/")) return { data: [BRANCH] }
    throw new Error(`unexpected GET ${url}`)
  })
}

function Wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
}

describe("users page helpers", () => {
  it("derives initials from the name, falling back to the e-mail", () => {
    expect(initials("Ayşe Yılmaz", "a@x")).toBe("AY")
    expect(initials("Ayşe", "a@x")).toBe("A")
    expect(initials("", "ilker@x")).toBe("İ")
  })

  it("filters by name/e-mail, role and branch", () => {
    const rows = [
      member(1, { person_name: "Ayşe Yılmaz", person_email: "ayse@x.com" }),
      member(2, { person_name: "Mehmet Kaya", role_id: ROLE_MANAGER.id, role_name: ROLE_MANAGER.name, branch_id: undefined }),
    ]
    expect(filterMembers(rows, { query: "ayşe", roleId: "", branchId: "" })).toHaveLength(1)
    expect(filterMembers(rows, { query: "KAYA", roleId: "", branchId: "" })[0].id).toBe("m-2")
    expect(filterMembers(rows, { query: "", roleId: ROLE_MANAGER.id, branchId: "" })[0].id).toBe("m-2")
    expect(filterMembers(rows, { query: "", roleId: "", branchId: "chain" })[0].id).toBe("m-2")
    expect(filterMembers(rows, { query: "", roleId: "", branchId: BRANCH.id })[0].id).toBe("m-1")
  })
})

describe("UsersPage", () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
    useAuthStore.setState({ tenantId: TENANT })
  })

  it("renders person names, e-mails, role names and the summary line", async () => {
    mockApi([
      member(1, { person_name: "Ayşe Yılmaz", person_email: "ayse@x.com", role_id: ROLE_MANAGER.id, role_name: "Yönetici", branch_id: undefined }),
      member(2, { status: "suspended" }),
    ])
    render(<UsersPage />, { wrapper: Wrapper })

    expect(await screen.findByText("Ayşe Yılmaz")).toBeInTheDocument()
    expect(screen.getByText("ayse@x.com")).toBeInTheDocument()
    expect(screen.getByTestId("members-summary")).toHaveTextContent("2 üye · 1 aktif · 1 pasif")

    const rows = screen.getAllByRole("row").slice(1)
    expect(within(rows[0]).getByText("Yönetici")).toBeInTheDocument()
    expect(within(rows[0]).getByText("Tüm şubeler")).toBeInTheDocument()
    expect(within(rows[0]).getByText("Aktif")).toBeInTheDocument()
    expect(within(rows[1]).getByText("Ana Şube")).toBeInTheDocument()
    expect(within(rows[1]).getByText("Pasif")).toBeInTheDocument()
    expect(screen.queryByText(/p-1/)).not.toBeInTheDocument()
  })

  it("narrows the table with the search box and role filter", async () => {
    mockApi([
      member(1, { person_name: "Ayşe Yılmaz" }),
      member(2, { person_name: "Mehmet Kaya", role_id: ROLE_MANAGER.id, role_name: "Yönetici" }),
    ])
    render(<UsersPage />, { wrapper: Wrapper })
    await screen.findByText("Ayşe Yılmaz")

    fireEvent.change(screen.getByLabelText("Ad veya e-posta ara"), { target: { value: "mehmet" } })
    expect(screen.queryByText("Ayşe Yılmaz")).not.toBeInTheDocument()
    expect(screen.getByText("Mehmet Kaya")).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText("Ad veya e-posta ara"), { target: { value: "" } })
    fireEvent.change(screen.getByLabelText("Rol süzgeci"), { target: { value: ROLE_CASHIER.id } })
    expect(screen.getByText("Ayşe Yılmaz")).toBeInTheDocument()
    expect(screen.queryByText("Mehmet Kaya")).not.toBeInTheDocument()

    fireEvent.change(screen.getByLabelText("Rol süzgeci"), { target: { value: ROLE_MANAGER.id } })
    fireEvent.change(screen.getByLabelText("Ad veya e-posta ara"), { target: { value: "yok" } })
    expect(screen.getByText("Süzgece uyan üye yok")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Süzgeçleri temizle" }))
    expect(screen.getByText("Mehmet Kaya")).toBeInTheDocument()
  })

  it("pages 25 rows at a time", async () => {
    mockApi(Array.from({ length: PAGE_SIZE + 5 }, (_, i) => member(i + 1)))
    render(<UsersPage />, { wrapper: Wrapper })
    await screen.findByText("Kişi 1")

    expect(screen.getAllByRole("row").slice(1)).toHaveLength(PAGE_SIZE)
    expect(screen.queryByText(`Kişi ${PAGE_SIZE + 1}`)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Sonraki" }))
    expect(screen.getAllByRole("row").slice(1)).toHaveLength(5)
    expect(screen.getByText(`Kişi ${PAGE_SIZE + 1}`)).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Sonraki" })).toBeDisabled()
  })

  it("suspends a member through PUT /memberships/{id} after confirmation", async () => {
    mockApi([member(1, { person_name: "Ayşe Yılmaz" })])
    put.mockResolvedValue({})
    render(<UsersPage />, { wrapper: Wrapper })
    await screen.findByText("Ayşe Yılmaz")

    fireEvent.pointerDown(screen.getByRole("button", { name: "Ayşe Yılmaz için işlemler" }))
    fireEvent.click(await screen.findByRole("menuitem", { name: "Pasife al" }))
    fireEvent.click(await screen.findByRole("button", { name: "Pasife al" }))

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(`/v1/identity/${TENANT}/memberships/m-1`, { status: "suspended" }),
    )
  })
})
