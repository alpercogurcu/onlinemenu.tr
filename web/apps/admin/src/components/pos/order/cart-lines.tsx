"use client"

import { Minus, Plus, Trash2, TriangleAlert } from "lucide-react"
import { useTranslations } from "next-intl"

import {
  MAX_LINE_QUANTITY,
  composeOptionNote,
  formatMoney,
  pendingLineTotal,
  type PendingLine,
} from "@onlinemenu/pos-core"

const stepClass =
  "flex size-12 items-center justify-center rounded-xl border-2 bg-card outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-40"

interface CartLinesProps {
  lines: PendingLine[]
  disabled?: boolean
  onChangeQuantity: (clientId: string, delta: number) => void
  onRemove: (clientId: string) => void
}

/**
 * The not-yet-sent round. Row layout follows spec §4 "Adisyon satırı": name
 * and its options on top, then [−] qty [+] and a separate, labelled "Sil" —
 * the stepper never deletes on its own (pos-core changePendingQuantity clamps
 * at 1), so a stray "−" cannot silently drop a line.
 */
export function CartLines({ lines, disabled, onChangeQuantity, onRemove }: CartLinesProps) {
  const t = useTranslations("posOrder.cart")
  const tp = useTranslations("posOrder")

  if (lines.length === 0) {
    return <p className="px-1 py-6 text-center text-base text-muted-foreground">{t("empty")}</p>
  }

  return (
    <ul className="divide-y" aria-label={t("title")}>
      {lines.map((line) => {
        const detail = composeOptionNote(line.modifiers, line.note)
        return (
          <li key={line.clientId} className="space-y-2 py-3" data-testid="cart-line">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <div className="text-base font-semibold break-words">{line.productName}</div>
                {detail && <div className="text-sm break-words text-muted-foreground">{detail}</div>}
                {line.optionsUnavailable && (
                  <div className="text-status-warning-fg mt-1 flex items-start gap-1 text-sm">
                    <TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                    {tp("optionsUnavailable")}
                  </div>
                )}
              </div>
              <div className="shrink-0 text-base font-bold tabular-nums">{formatMoney(pendingLineTotal(line))}</div>
            </div>
            <div className="flex items-center justify-between gap-3">
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  className={stepClass}
                  aria-label={t("decrease", { product: line.productName })}
                  disabled={disabled || line.quantity <= 1}
                  onClick={() => onChangeQuantity(line.clientId, -1)}
                >
                  <Minus className="size-5" />
                </button>
                <span className="min-w-8 text-center text-xl font-bold tabular-nums" aria-label={t("quantity")}>
                  {line.quantity}
                </span>
                <button
                  type="button"
                  className={stepClass}
                  aria-label={t("increase", { product: line.productName })}
                  disabled={disabled || line.quantity >= MAX_LINE_QUANTITY}
                  onClick={() => onChangeQuantity(line.clientId, 1)}
                >
                  <Plus className="size-5" />
                </button>
              </div>
              <button
                type="button"
                disabled={disabled}
                aria-label={t("removeAria", { product: line.productName })}
                onClick={() => onRemove(line.clientId)}
                className="text-status-danger-fg flex min-h-12 items-center gap-2 rounded-xl px-3 text-base font-medium outline-none hover:bg-status-danger-bg focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-40"
              >
                <Trash2 className="size-5" aria-hidden="true" />
                {t("remove")}
              </button>
            </div>
          </li>
        )
      })}
    </ul>
  )
}
