"use client"

import { Check, Minus, PencilLine, Plus } from "lucide-react"
import { useTranslations } from "next-intl"
import { useRef, useState } from "react"

import {
  MAX_LINE_QUANTITY,
  QUICK_NOTES,
  composeFreeNote,
  defaultSelection,
  formatDelta,
  formatMoney,
  groupHint,
  selectedModifiers,
  toggleModifier,
  unitPriceWith,
  unmetGroupIds,
  type LineOptions,
  type ModifierGroupSource,
  type OptionSelection,
  type ProductSource,
} from "@onlinemenu/pos-core"

import { TouchSheet } from "@/components/pos/order/touch-sheet"
import { cn } from "@/lib/utils"

interface OptionPanelProps {
  product: ProductSource
  groups: ModifierGroupSource[]
  onConfirm: (options: Required<LineOptions>) => void
  onCancel: () => void
}

function chipClass(active: boolean) {
  return cn(
    "inline-flex min-h-12 items-center gap-2 rounded-xl border-2 px-4 text-base font-medium transition-colors motion-reduce:transition-none",
    "focus-visible:ring-ring/50 outline-none focus-visible:ring-[3px] active:scale-[0.98] motion-reduce:active:scale-100",
    active ? "border-primary bg-primary/10 text-foreground" : "border-border bg-card text-foreground hover:bg-accent",
  )
}

const stepClass =
  "flex size-12 items-center justify-center rounded-xl border-2 bg-card text-foreground outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-40"

/**
 * Option picker for the web order screen (docs/pos-ux-spec.md §3a). Same rules
 * as pos-desktop's OptionPicker — every rule comes from @onlinemenu/pos-core —
 * but laid out for a phone: bottom sheet, one group per block, chips that
 * wrap, a footer that never scrolls away.
 *
 * Mounted per product, so the state always starts on the fast path: required
 * groups pre-selected, quantity 1 — "Sepete ekle" is the second tap. An
 * unanswered required group does not disable the button (spec ilke 5): the tap
 * marks the group and scrolls to it instead.
 */
export function OptionPanel({ product, groups, onConfirm, onCancel }: OptionPanelProps) {
  const t = useTranslations("posOrder.picker")
  const [selection, setSelection] = useState<OptionSelection>(() => defaultSelection(groups))
  const [quantity, setQuantity] = useState(1)
  const [noteChips, setNoteChips] = useState<string[]>([])
  const [customNote, setCustomNote] = useState("")
  const [customNoteShown, setCustomNoteShown] = useState(false)
  const [showUnmet, setShowUnmet] = useState(false)
  const [blockedGroupId, setBlockedGroupId] = useState<string | null>(null)
  const groupRefs = useRef(new Map<string, HTMLElement>())

  const chosen = selectedModifiers(groups, selection)
  const unmet = unmetGroupIds(groups, selection)
  const lineTotal = unitPriceWith(product.price_amount, chosen) * quantity

  function handleToggle(group: ModifierGroupSource, modifierId: string) {
    const { next, blocked } = toggleModifier(group, selection[group.id] ?? [], modifierId)
    setSelection((current) => ({ ...current, [group.id]: next }))
    setBlockedGroupId(blocked ? group.id : null)
  }

  function toggleNoteChip(chip: string) {
    setNoteChips((current) => (current.includes(chip) ? current.filter((c) => c !== chip) : [...current, chip]))
  }

  function handleConfirm() {
    if (unmet.length > 0) {
      setShowUnmet(true)
      groupRefs.current.get(unmet[0])?.scrollIntoView?.({ block: "nearest", behavior: "smooth" })
      return
    }
    onConfirm({ modifiers: chosen, note: composeFreeNote(noteChips, customNote), quantity })
  }

  return (
    <TouchSheet
      open
      onOpenChange={(next) => {
        if (!next) onCancel()
      }}
      title={product.name}
      closeLabel={t("close")}
      aside={
        <span className="shrink-0 text-base font-semibold text-muted-foreground tabular-nums">
          {formatMoney(product.price_amount)}
        </span>
      }
      footer={
        <div className="space-y-3">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2" role="group" aria-label={t("quantity")}>
              <button
                type="button"
                className={stepClass}
                aria-label={t("decrease")}
                disabled={quantity <= 1}
                onClick={() => setQuantity((q) => Math.max(1, q - 1))}
              >
                <Minus className="size-5" />
              </button>
              <span className="min-w-10 text-center text-2xl font-bold tabular-nums" aria-live="polite">
                {quantity}
              </span>
              <button
                type="button"
                className={stepClass}
                aria-label={t("increase")}
                disabled={quantity >= MAX_LINE_QUANTITY}
                onClick={() => setQuantity((q) => Math.min(MAX_LINE_QUANTITY, q + 1))}
              >
                <Plus className="size-5" />
              </button>
            </div>
            <div className="text-right">
              <div className="text-sm text-muted-foreground">{t("lineTotal")}</div>
              <div className="text-2xl font-bold tabular-nums" data-testid="option-line-total">
                {formatMoney(lineTotal)}
              </div>
            </div>
          </div>
          <div className="flex gap-3">
            <button
              type="button"
              onClick={onCancel}
              className="min-h-14 flex-1 rounded-xl border-2 text-base font-semibold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
            >
              {t("cancel")}
            </button>
            <button
              type="button"
              onClick={handleConfirm}
              className="bg-primary text-primary-foreground min-h-14 flex-[2] rounded-xl text-lg font-bold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 active:scale-[0.99]"
            >
              {t("add")}
            </button>
          </div>
        </div>
      }
    >
      <div className="space-y-5 p-4">
        {groups.map((group) => {
          const isUnmet = showUnmet && unmet.includes(group.id)
          const picked = selection[group.id] ?? []
          const single = group.selection_type === "single"
          return (
            <section
              key={group.id}
              ref={(el) => {
                if (el) groupRefs.current.set(group.id, el)
                else groupRefs.current.delete(group.id)
              }}
              aria-labelledby={`og-${group.id}`}
              className={cn(
                "-mx-2 rounded-xl border-2 px-2 py-2",
                isUnmet ? "border-status-warning-border bg-status-warning-bg" : "border-transparent",
              )}
            >
              <h3 id={`og-${group.id}`} className="mb-2 text-base font-semibold">
                {group.name}{" "}
                <span className="text-sm font-normal text-muted-foreground">{`(${groupHint(group)})`}</span>
              </h3>
              <div className="flex flex-wrap gap-2" role={single ? "radiogroup" : "group"} aria-labelledby={`og-${group.id}`}>
                {group.modifiers.map((modifier) => {
                  const active = picked.includes(modifier.id)
                  const delta = formatDelta(modifier.price_delta)
                  return (
                    <button
                      key={modifier.id}
                      type="button"
                      role={single ? "radio" : undefined}
                      aria-checked={single ? active : undefined}
                      aria-pressed={single ? undefined : active}
                      onClick={() => handleToggle(group, modifier.id)}
                      className={chipClass(active)}
                    >
                      {active && <Check className="text-primary size-5" aria-hidden="true" />}
                      <span>{modifier.name}</span>
                      {delta && <span className="text-muted-foreground tabular-nums">{delta}</span>}
                    </button>
                  )
                })}
              </div>
              {isUnmet && (
                <p role="alert" className="text-status-warning-fg mt-2 text-sm font-semibold">
                  {t("required")}
                </p>
              )}
              {blockedGroupId === group.id && (
                <p role="status" className="mt-2 text-sm text-muted-foreground">
                  {t("maxReached", { max: group.max_selections })}
                </p>
              )}
            </section>
          )
        })}

        <section aria-labelledby="og-note">
          <h3 id="og-note" className="mb-2 text-base font-semibold">
            {t("note")}
          </h3>
          <div className="flex flex-wrap gap-2">
            {QUICK_NOTES.map((chip) => (
              <button
                key={chip}
                type="button"
                aria-pressed={noteChips.includes(chip)}
                onClick={() => toggleNoteChip(chip)}
                className={chipClass(noteChips.includes(chip))}
              >
                {noteChips.includes(chip) && <Check className="text-primary size-5" aria-hidden="true" />}
                {chip}
              </button>
            ))}
            <button
              type="button"
              aria-expanded={customNoteShown}
              onClick={() => setCustomNoteShown((shown) => !shown)}
              className={cn(chipClass(customNoteShown), !customNoteShown && "border-dashed")}
            >
              <PencilLine className="size-5" aria-hidden="true" />
              {t("customNote")}
            </button>
          </div>
          {customNoteShown && (
            <input
              type="text"
              autoFocus
              value={customNote}
              onChange={(e) => setCustomNote(e.target.value)}
              aria-label={t("customNote")}
              placeholder={t("customNotePlaceholder")}
              maxLength={120}
              // 16px+ text: iOS Safari zooms the page on focus below that.
              className="bg-card focus-visible:ring-ring/50 mt-3 min-h-12 w-full rounded-xl border-2 px-3 text-base outline-none focus-visible:ring-[3px]"
            />
          )}
        </section>
      </div>
    </TouchSheet>
  )
}
