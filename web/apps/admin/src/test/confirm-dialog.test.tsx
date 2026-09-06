// ConfirmDialog is the shared destructive-action gate used across the
// catalog UX (delete product, delete modifier group, deactivate-instead...).
// The one behavior worth a regression test is that the confirm button stays
// disabled for the whole lifetime of an async onConfirm — a caller that
// awaits a DELETE request must not let a second click fire a second request
// while the first is still in flight.
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { ConfirmDialog } from "@/components/catalog/confirm-dialog"

describe("ConfirmDialog", () => {
  it("calls onConfirm and disables the confirm button until it settles", async () => {
    let resolveConfirm!: () => void
    const onConfirm = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveConfirm = resolve
        }),
    )
    const onOpenChange = vi.fn()

    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title={'"Kola" silinsin mi?'}
        confirmLabel="Sil"
        cancelLabel="Vazgeç"
        destructive
        onConfirm={onConfirm}
      />,
    )

    const confirmButton = screen.getByRole("button", { name: "Sil" })
    fireEvent.click(confirmButton)

    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(confirmButton).toBeDisabled()
    expect(onOpenChange).not.toHaveBeenCalled()

    resolveConfirm()

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
  })

  it("keeps the dialog open and re-enables the confirm button when onConfirm rejects", async () => {
    let rejectConfirm!: (err: unknown) => void
    const error = new Error("delete failed")
    const onConfirm = vi.fn(
      () =>
        new Promise<void>((_resolve, reject) => {
          rejectConfirm = reject
        }),
    )
    const onOpenChange = vi.fn()
    const onError = vi.fn()

    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="Silinsin mi?"
        confirmLabel="Sil"
        cancelLabel="Vazgeç"
        destructive
        onConfirm={onConfirm}
        onError={onError}
      />,
    )

    const confirmButton = screen.getByRole("button", { name: "Sil" })
    fireEvent.click(confirmButton)
    expect(confirmButton).toBeDisabled()

    rejectConfirm(error)

    await waitFor(() => expect(confirmButton).not.toBeDisabled())

    expect(onOpenChange).not.toHaveBeenCalled()
    expect(onError).toHaveBeenCalledWith(error)
  })

  it("ignores Escape while a confirm is pending, but allows closing once it settles", async () => {
    let resolveConfirm!: () => void
    const onConfirm = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveConfirm = resolve
        }),
    )
    const onOpenChange = vi.fn()

    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="Silinsin mi?"
        confirmLabel="Sil"
        cancelLabel="Vazgeç"
        destructive
        onConfirm={onConfirm}
      />,
    )

    fireEvent.click(screen.getByRole("button", { name: "Sil" }))

    fireEvent.keyDown(document, { key: "Escape" })
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(screen.getByRole("button", { name: "Sil" })).toBeInTheDocument()

    resolveConfirm()

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
  })

  it("does not call onConfirm and closes when cancel is clicked", () => {
    const onConfirm = vi.fn()
    const onOpenChange = vi.fn()

    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="Grup silinsin mi?"
        confirmLabel="Sil"
        cancelLabel="Vazgeç"
        onConfirm={onConfirm}
      />,
    )

    fireEvent.click(screen.getByRole("button", { name: "Vazgeç" }))

    expect(onConfirm).not.toHaveBeenCalled()
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it("renders and wires up the secondary action, closing the dialog after it runs", () => {
    const onConfirm = vi.fn()
    const onOpenChange = vi.fn()
    const secondaryOnClick = vi.fn()

    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title={'"Kola" silinsin mi?'}
        confirmLabel="Sil"
        cancelLabel="Vazgeç"
        destructive
        onConfirm={onConfirm}
        secondaryAction={{ label: "Satıştan kaldır", onClick: secondaryOnClick }}
      />,
    )

    fireEvent.click(screen.getByRole("button", { name: "Satıştan kaldır" }))

    expect(secondaryOnClick).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it("does not render a secondary action button when none is given", () => {
    render(
      <ConfirmDialog
        open
        onOpenChange={vi.fn()}
        title="Sil?"
        confirmLabel="Sil"
        cancelLabel="Vazgeç"
        onConfirm={vi.fn()}
      />,
    )

    expect(screen.queryByRole("button", { name: "Satıştan kaldır" })).not.toBeInTheDocument()
  })

  it("renders the title and description", () => {
    render(
      <ConfirmDialog
        open
        onOpenChange={vi.fn()}
        title={'"Kola" silinsin mi?'}
        description="Bu işlem geri alınamaz."
        confirmLabel="Sil"
        cancelLabel="Vazgeç"
        onConfirm={vi.fn()}
      />,
    )

    expect(screen.getByText("\"Kola\" silinsin mi?")).toBeInTheDocument()
    expect(screen.getByText("Bu işlem geri alınamaz.")).toBeInTheDocument()
  })
})
