// Native radio-group implementation — @radix-ui/react-radio-group is not
// installed. Mirrors the switch.tsx / select.tsx approach: a plain form
// control styled to match the design system, sharing a value/onValueChange
// API so call sites read the same as the radix version would.
"use client"

import * as React from "react"

import { cn } from "@/lib/utils"

interface RadioGroupContextValue {
  name: string
  value?: string
  onValueChange?: (value: string) => void
}

const RadioGroupContext = React.createContext<RadioGroupContextValue | null>(null)

interface RadioGroupProps {
  name: string
  value?: string
  onValueChange?: (value: string) => void
  className?: string
  children: React.ReactNode
}

function RadioGroup({ name, value, onValueChange, className, children }: RadioGroupProps) {
  return (
    <RadioGroupContext.Provider value={{ name, value, onValueChange }}>
      <div role="radiogroup" className={cn("grid gap-2", className)}>
        {children}
      </div>
    </RadioGroupContext.Provider>
  )
}

interface RadioGroupItemProps {
  value: string
  id: string
  className?: string
  disabled?: boolean
}

function RadioGroupItem({ value, id, className, disabled }: RadioGroupItemProps) {
  const ctx = React.useContext(RadioGroupContext)
  if (!ctx) {
    throw new Error("RadioGroupItem must be used within a RadioGroup")
  }

  return (
    <input
      type="radio"
      id={id}
      name={ctx.name}
      value={value}
      checked={ctx.value === value}
      disabled={disabled}
      onChange={() => ctx.onValueChange?.(value)}
      className={cn(
        "size-4 shrink-0 border-input text-primary focus-visible:ring-ring/50 focus-visible:ring-[3px] focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
    />
  )
}

export { RadioGroup, RadioGroupItem }
