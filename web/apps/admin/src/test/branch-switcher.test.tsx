// The header BranchSwitcher: one change there feeds EVERY page that reads
// use-selected-branch (the whole point of the refactor — "Masalar'da
// Serdivan'a çevirdim, Adisyonlar da Serdivan gelsin"), and a branch-scoped
// role gets a read-only badge instead of a Select.
import { fireEvent, render, screen } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

let branches: { id: string; name: string }[] = []
vi.mock("@/hooks/use-tenant", () => ({
  useBranches: () => ({ data: branches, isLoading: false }),
}))

let scopedBranchId: string | null = null
vi.mock("@/lib/permissions", () => ({ currentBranchId: () => scopedBranchId }))

import BranchSwitcher from "@/components/layouts/branch-switcher"
import { useSelectedBranch } from "@/hooks/use-selected-branch"
import { useAuthStore } from "@/store/auth-store"
import { useBranchStore } from "@/store/branch-store"

// Stand-ins for two separate branch-keyed pages (Masalar / Adisyonlar):
// both must follow the single global selection.
function PageA() {
  const { branchId } = useSelectedBranch()
  return <div data-testid="page-a">{branchId}</div>
}

function PageB() {
  const { branch } = useSelectedBranch()
  return <div data-testid="page-b">{branch?.name ?? ""}</div>
}

beforeEach(() => {
  branches = [
    { id: "b1", name: "Adapazarı" },
    { id: "b2", name: "Serdivan" },
  ]
  scopedBranchId = null
  useBranchStore.setState({ selections: {} })
  useAuthStore.setState({ user: { id: "u1", name: "Test", email: "t@x" }, tenantId: "t1" })
})

describe("BranchSwitcher", () => {
  it("one switch in the header lands on every page hook at once", () => {
    render(
      <>
        <BranchSwitcher />
        <PageA />
        <PageB />
      </>,
    )

    expect(screen.getByTestId("page-a")).toHaveTextContent("b1")
    expect(screen.getByTestId("page-b")).toHaveTextContent("Adapazarı")

    fireEvent.change(screen.getByRole("combobox", { name: "Şube seçimi" }), { target: { value: "b2" } })

    expect(screen.getByTestId("page-a")).toHaveTextContent("b2")
    expect(screen.getByTestId("page-b")).toHaveTextContent("Serdivan")
  })

  it("renders a locked badge (no Select) for a branch-scoped role", () => {
    scopedBranchId = "b2"
    render(
      <>
        <BranchSwitcher />
        <PageA />
      </>,
    )

    expect(screen.queryByRole("combobox", { name: "Şube seçimi" })).not.toBeInTheDocument()
    expect(screen.getByTestId("branch-switcher-locked")).toHaveTextContent("Serdivan")
    expect(screen.getByTestId("page-a")).toHaveTextContent("b2")
  })

  it("renders nothing while there is no branch list yet", () => {
    branches = []
    const { container } = render(<BranchSwitcher />)
    expect(container).toBeEmptyDOMElement()
  })
})
