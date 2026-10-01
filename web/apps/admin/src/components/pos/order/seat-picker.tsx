"use client"

import { Plus } from "lucide-react"
import { useTranslations } from "next-intl"

import { cn } from "@/lib/utils"

/** A stuck "+" finger must not produce a hundred chips; no table seats more. */
export const MAX_SEATS = 20

interface SeatPickerProps {
  count: number
  active: number
  onSelect: (seat: number) => void
  onAdd: () => void
  className?: string
}

/**
 * Guest (kuver) chips for the dine-in order screen: every new product lands on
 * the ACTIVE guest, and the selection sticks until the waiter changes it — the
 * common "everything for guest 2 now" round costs one tap, not one per item.
 * "+" appends the next number and makes it active. 44px touch targets
 * (docs/pos-ux-spec.md); radiogroup so a screen reader announces the choice.
 */
export function SeatPicker({ count, active, onSelect, onAdd, className }: SeatPickerProps) {
  const t = useTranslations("posOrder.seats")
  return (
    <div className={cn("flex items-center gap-3", className)} data-testid="seat-picker">
      <span className="shrink-0 text-sm font-semibold text-muted-foreground">{t("label")}</span>
      <div role="radiogroup" aria-label={t("label")} className="flex min-w-0 flex-1 items-center gap-2 overflow-x-auto py-0.5">
        {Array.from({ length: count }, (_, i) => i + 1).map((n) => (
          <button
            key={n}
            type="button"
            role="radio"
            aria-checked={n === active}
            aria-label={t("person", { n })}
            onClick={() => onSelect(n)}
            className={cn(
              "flex size-11 shrink-0 items-center justify-center rounded-full border-2 text-base font-bold tabular-nums outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
              n === active ? "border-primary bg-primary text-primary-foreground" : "bg-card",
            )}
          >
            {n}
          </button>
        ))}
        {count < MAX_SEATS && (
          <button
            type="button"
            aria-label={t("add")}
            onClick={onAdd}
            className="flex size-11 shrink-0 items-center justify-center rounded-full border-2 border-dashed bg-card text-muted-foreground outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
          >
            <Plus className="size-5" aria-hidden="true" />
          </button>
        )}
      </div>
    </div>
  )
}

/** 24px guest badge at the head of a cart line; seat 0 (service checks) renders nothing. */
export function SeatBadge({ seat }: { seat: number }) {
  const t = useTranslations("posOrder.seats")
  if (seat <= 0) return null
  return (
    <span
      aria-label={t("person", { n: seat })}
      className="border-primary/40 bg-primary/10 text-primary mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full border text-xs font-bold tabular-nums"
    >
      {seat}
    </span>
  )
}
