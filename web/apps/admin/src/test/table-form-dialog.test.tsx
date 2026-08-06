// Contract test for the table creation form: the payload it POSTs is the one
// backend/internal/modules/pos/http/handler.go's createTable decodes
// (branch_id + zone_id + name required, 422 otherwise), and the form must not
// fire that request at all when the operator left a required field empty.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { TableFormDialog } from "@/components/pos/table-form-dialog"
import messages from "@/messages/tr.json"
import type { PosZone } from "@/types"

const post = vi.fn()
const patch = vi.fn()

vi.mock("@/lib/api", () => ({
  default: {
    post: (...args: unknown[]) => post(...args),
    patch: (...args: unknown[]) => patch(...args),
  },
}))

const toastError = vi.fn()
vi.mock("sonner", () => ({
  toast: {
    success: vi.fn(),
    error: (...args: unknown[]) => toastError(...args),
  },
}))

const BRANCH_ID = "bbbbbbbb-0000-0000-0000-000000000001"
const ZONE: PosZone = {
  id: "4286b42d-b259-42a5-bae2-ab87b4b4c875",
  branch_id: BRANCH_ID,
  name: "Teras",
  floor: 1,
  is_active: true,
}

function Wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <NextIntlClientProvider locale="tr" messages={messages}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </NextIntlClientProvider>
  )
}

function renderDialog(zones: PosZone[] = [ZONE]) {
  return render(
    <TableFormDialog open onOpenChange={() => {}} branchId={BRANCH_ID} zones={zones} />,
    { wrapper: Wrapper },
  )
}

describe("TableFormDialog", () => {
  beforeEach(() => {
    post.mockReset()
    post.mockResolvedValue({ data: {} })
    patch.mockReset()
    toastError.mockReset()
  })

  it("posts branch, zone, name and capacity to the create endpoint", async () => {
    renderDialog()

    fireEvent.change(screen.getByLabelText("Masa adı"), { target: { value: " T1 " } })
    fireEvent.change(screen.getByLabelText("Kapasite (kişi)"), { target: { value: "6" } })
    fireEvent.click(screen.getByRole("button", { name: "Kaydet" }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith("/api/v1/pos/tables", {
      branch_id: BRANCH_ID,
      zone_id: ZONE.id,
      name: "T1",
      capacity: 6,
    })
  })

  it("does not call the API when the name is blank", async () => {
    renderDialog()

    fireEvent.click(screen.getByRole("button", { name: "Kaydet" }))

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Masa adı zorunludur"))
    expect(post).not.toHaveBeenCalled()
  })

  // A cleared capacity field is the case native validation does NOT catch:
  // `min={1}` rejects "0" before submit ever fires, but an empty numeric input
  // is considered valid, and Number("") is 0 — so without the explicit guard a
  // zero-capacity table would be created.
  it("rejects an emptied capacity instead of sending zero", async () => {
    renderDialog()

    fireEvent.change(screen.getByLabelText("Masa adı"), { target: { value: "T1" } })
    fireEvent.change(screen.getByLabelText("Kapasite (kişi)"), { target: { value: "" } })
    fireEvent.click(screen.getByRole("button", { name: "Kaydet" }))

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Kapasite en az 1 olmalıdır"))
    expect(post).not.toHaveBeenCalled()
  })

  it("explains what to do first when the branch has no zone", () => {
    renderDialog([])

    expect(
      screen.getByText("Önce bir bölge oluşturun; masa bir bölgeye bağlanmak zorunda."),
    ).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Kaydet" })).not.toBeInTheDocument()
  })
})
