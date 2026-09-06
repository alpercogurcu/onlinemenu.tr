// MoneyInput wraps lib/money.ts's parseLiraToKurus/formatKurusForInput —
// it must never re-implement lira<->kurus parsing itself. Rendered through a
// small controlled harness (state lives in the test, like a real parent form)
// rather than a bare presentational render, so that blur-time reformatting —
// driven by the *current* valueKurus prop — can be exercised the same way
// ProductForm/ModifierOptionsEditor would use it.
import { fireEvent, render, screen } from "@testing-library/react"
import { useState } from "react"
import { describe, expect, it, vi } from "vitest"

import { MoneyInput } from "@/components/catalog/money-input"

function renderHarness(initial: number | null, onChangeKurus: (v: number | null) => void, allowNegative?: boolean) {
  function Inner() {
    const [value, setValue] = useState<number | null>(initial)
    return (
      <MoneyInput
        id="price"
        valueKurus={value}
        allowNegative={allowNegative}
        onChangeKurus={(v) => {
          onChangeKurus(v)
          setValue(v)
        }}
      />
    )
  }
  return render(<Inner />)
}

describe("MoneyInput", () => {
  it("parses a typed Turkish decimal comma into kurus", () => {
    const onChangeKurus = vi.fn()
    renderHarness(null, onChangeKurus)

    fireEvent.change(screen.getByRole("textbox"), { target: { value: "12,50" } })

    expect(onChangeKurus).toHaveBeenCalledWith(1250)
  })

  it("reports an emptied field as null", () => {
    const onChangeKurus = vi.fn()
    renderHarness(1250, onChangeKurus)

    fireEvent.change(screen.getByRole("textbox"), { target: { value: "" } })

    expect(onChangeKurus).toHaveBeenCalledWith(null)
  })

  it("rejects a negative amount when allowNegative is not set", () => {
    const onChangeKurus = vi.fn()
    renderHarness(null, onChangeKurus)

    fireEvent.change(screen.getByRole("textbox"), { target: { value: "-12,50" } })

    expect(onChangeKurus).toHaveBeenCalledWith(null)
    expect(onChangeKurus).not.toHaveBeenCalledWith(-1250)
  })

  it("accepts a negative amount when allowNegative is set", () => {
    const onChangeKurus = vi.fn()
    renderHarness(null, onChangeKurus, true)

    fireEvent.change(screen.getByRole("textbox"), { target: { value: "-12,50" } })

    expect(onChangeKurus).toHaveBeenCalledWith(-1250)
  })

  it("reformats to the canonical lira string on blur", () => {
    const onChangeKurus = vi.fn()
    renderHarness(null, onChangeKurus)

    const input = screen.getByRole("textbox") as HTMLInputElement
    fireEvent.change(input, { target: { value: "12.5" } })
    expect(onChangeKurus).toHaveBeenCalledWith(1250)

    fireEvent.blur(input)

    expect(input.value).toBe("12,50")
  })

  it("keeps the raw text while the field is focused, without reformatting mid-typing", () => {
    const onChangeKurus = vi.fn()
    renderHarness(1250, onChangeKurus)

    const input = screen.getByRole("textbox") as HTMLInputElement
    fireEvent.change(input, { target: { value: "12," } })

    expect(input.value).toBe("12,")
  })
})
