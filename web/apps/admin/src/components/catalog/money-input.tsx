"use client"

import * as React from "react"

import { Input } from "@/components/ui/input"
import { formatKurusForInput, parseLiraToKurus } from "@/lib/money"

interface MoneyInputProps {
  id?: string
  valueKurus: number | null
  onChangeKurus: (value: number | null) => void
  allowNegative?: boolean
  placeholder?: string
  "aria-invalid"?: boolean
  "aria-describedby"?: string
}

// parseLiraToKurus (lib/money.ts) always rejects a leading "-" — a price
// override or a menu price has no meaning below zero. A modifier's
// price_delta is the one field allowed to go negative (a "no cheese"
// discount), so allowNegative peels the sign off first and negates the
// magnitude parseLiraToKurus returns, rather than re-implementing the parse.
function parse(raw: string, allowNegative: boolean): number | null {
  const trimmed = raw.trim()
  const negative = trimmed.startsWith("-")
  if (negative && !allowNegative) return null

  const magnitude = parseLiraToKurus(negative ? trimmed.slice(1) : trimmed)
  if (magnitude === null) return null
  return negative ? -magnitude : magnitude
}

function display(valueKurus: number | null): string {
  return valueKurus == null ? "" : formatKurusForInput(valueKurus)
}

// Controlled kuruş<->TL field. While the input has focus the user's raw text
// is shown as typed (so "12," is not clobbered into "12,00" mid-keystroke);
// on blur it snaps back to the canonical formatKurusForInput rendering of
// whatever valueKurus the parent settled on.
export function MoneyInput({
  id,
  valueKurus,
  onChangeKurus,
  allowNegative = false,
  placeholder,
  "aria-invalid": ariaInvalid,
  "aria-describedby": ariaDescribedBy,
}: MoneyInputProps) {
  const [text, setText] = React.useState(() => display(valueKurus))
  const isEditingRef = React.useRef(false)

  React.useEffect(() => {
    if (isEditingRef.current) return
    setText(display(valueKurus))
  }, [valueKurus])

  return (
    <Input
      id={id}
      inputMode="decimal"
      placeholder={placeholder}
      aria-invalid={ariaInvalid}
      aria-describedby={ariaDescribedBy}
      value={text}
      onChange={(e) => {
        isEditingRef.current = true
        setText(e.target.value)
        onChangeKurus(parse(e.target.value, allowNegative))
      }}
      onBlur={() => {
        isEditingRef.current = false
        setText(display(valueKurus))
      }}
    />
  )
}
