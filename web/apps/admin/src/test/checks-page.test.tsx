// Adisyonlar list as a cashier uses it (2026-09-23 prod sweep): open checks
// first (the history was 20 closed/cancelled rows deep), "İptal" behind a
// confirmation, and a "Kapat" refusal that says why.
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { AxiosError, AxiosHeaders, type AxiosResponse } from "axios"
import { NextIntlClientProvider } from "next-intl"
import { beforeEach, describe, expect, it, vi } from "vitest"

import messages from "@/messages/tr.json"
import type { Check } from "@/types"

const closeMutate = vi.fn()
const cancelMutate = vi.fn()
let checks: Check[] = []

vi.mock("@/hooks/use-can", () => ({ useCan: () => true }))
vi.mock("@/hooks/use-pos", () => ({
  useChecks: () => ({ data: checks, isLoading: false }),
  useCloseCheck: () => ({ mutateAsync: closeMutate, isPending: false }),
  useCancelCheck: () => ({ mutateAsync: cancelMutate, isPending: false }),
}))
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }) }))

const toastError = vi.fn()
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: (...a: unknown[]) => toastError(...a) } }))

import ChecksPage from "@/app/(main)/pos/checks/page"

function check(id: string, status: Check["status"], label: string): Check {
  return {
    id,
    tenant_id: "t",
    branch_id: "b",
    table_label: label,
    pax: 2,
    status,
    note: "",
    opened_at: "2026-09-23T10:00:00Z",
    closed_at: status === "open" ? null : "2026-09-23T10:30:00Z",
    total: 47_000,
  }
}

function httpError(status: number, data: unknown): AxiosError {
  const config = { headers: new AxiosHeaders() }
  const response = { status, data, statusText: "", headers: {}, config } as AxiosResponse
  return new AxiosError(`Request failed with status code ${status}`, "ERR_BAD_RESPONSE", config, {}, response)
}

function renderPage() {
  return render(
    <NextIntlClientProvider locale="tr" messages={messages}>
      <ChecksPage />
    </NextIntlClientProvider>,
  )
}

describe("ChecksPage", () => {
  beforeEach(() => {
    closeMutate.mockReset()
    cancelMutate.mockReset()
    toastError.mockReset()
    checks = [check("c1", "open", "Masa 1"), check("c2", "closed", "Masa 2"), check("c3", "cancelled", "Masa 3")]
  })

  it("lists only open checks by default and shows the rest on 'Tümü'", () => {
    renderPage()
    expect(screen.getByRole("link", { name: /Masa 1/ })).toBeInTheDocument()
    expect(screen.queryByRole("link", { name: /Masa 2/ })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: /Tümü/ }))
    expect(screen.getByRole("link", { name: /Masa 2/ })).toBeInTheDocument()
    expect(screen.getByRole("link", { name: /Masa 3/ })).toBeInTheDocument()
  })

  it("explains an empty open list and offers the history", () => {
    checks = [check("c2", "closed", "Masa 2")]
    renderPage()
    expect(screen.getByText("Açık adisyon yok")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Tüm adisyonları göster" }))
    expect(screen.getByRole("link", { name: /Masa 2/ })).toBeInTheDocument()
  })

  it("asks before cancelling a check", async () => {
    cancelMutate.mockResolvedValue({})
    renderPage()

    fireEvent.click(screen.getByRole("button", { name: "İptal" }))
    expect(cancelMutate).not.toHaveBeenCalled()

    const dialog = await screen.findByRole("alertdialog")
    fireEvent.click(within(dialog).getByRole("button", { name: "Adisyonu iptal et" }))
    await waitFor(() => expect(cancelMutate).toHaveBeenCalledWith("c1"))
  })

  it("tells the cashier why an unpaid check cannot be closed", async () => {
    closeMutate.mockRejectedValue(httpError(409, { error: "x", code: "insufficient_payment" }))
    renderPage()

    fireEvent.click(screen.getByRole("button", { name: "Kapat" }))
    await waitFor(() => expect(toastError).toHaveBeenCalled())
    const [title, options] = toastError.mock.calls[0] as [string, { description: string }]
    expect(title).toBe("Adisyon kapatılamadı")
    expect(options.description).toContain("POS")
  })
})
