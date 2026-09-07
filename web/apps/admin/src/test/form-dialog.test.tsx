// FormDialog is the shared card-dialog wrapper replacing the right-side Sheet
// on every "Yeni …" form. The one behavior worth a regression test is `busy`:
// while a submit is in flight, Escape/outside-click must not close the
// dialog and the close button must be disabled — otherwise a second submit
// could fire while the first is still pending.
import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { FormDialog } from "@/components/layouts/form-dialog"

describe("FormDialog", () => {
  it("renders the title, description, children and footer", () => {
    render(
      <FormDialog
        open
        onOpenChange={() => {}}
        title="Yeni Şube"
        description="İşletmenize yeni bir şube ekleyin."
        footer={<button type="button">Kaydet</button>}
      >
        <p>Form içeriği</p>
      </FormDialog>,
    )

    expect(screen.getByText("Yeni Şube")).toBeInTheDocument()
    expect(screen.getByText("İşletmenize yeni bir şube ekleyin.")).toBeInTheDocument()
    expect(screen.getByText("Form içeriği")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Kaydet" })).toBeInTheDocument()
  })

  it("blocks Escape and disables the close button while busy", () => {
    const onOpenChange = vi.fn()
    render(
      <FormDialog open onOpenChange={onOpenChange} title="Yeni Şube" busy>
        <p>Form içeriği</p>
      </FormDialog>,
    )

    fireEvent.keyDown(document, { key: "Escape" })
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled()
  })

  it("closes on Escape when not busy", () => {
    const onOpenChange = vi.fn()
    render(
      <FormDialog open onOpenChange={onOpenChange} title="Yeni Şube">
        <p>Form içeriği</p>
      </FormDialog>,
    )

    fireEvent.keyDown(document, { key: "Escape" })
    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(screen.getByRole("button", { name: "Close" })).not.toBeDisabled()
  })
})
