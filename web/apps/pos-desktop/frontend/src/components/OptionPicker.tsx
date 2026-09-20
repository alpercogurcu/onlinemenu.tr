import { useEffect, useRef, useState } from 'react'
import { MAX_LINE_QUANTITY, type LineOptions, type ProductSource } from '../lib/cart'
import { formatMoney } from '../lib/format'
import {
  QUICK_NOTES,
  composeFreeNote,
  defaultSelection,
  formatDelta,
  groupHint,
  selectedModifiers,
  toggleModifier,
  unitPriceWith,
  unmetGroupIds,
  type ModifierGroupSource,
  type OptionSelection,
} from '../lib/options'

type OptionPickerProps = {
  product: ProductSource & { modifier_groups: ModifierGroupSource[] }
  onConfirm: (options: Required<LineOptions>) => void
  onCancel: () => void
}

const STEP_BUTTON_CLASS =
  'flex min-h-12 min-w-12 items-center justify-center rounded-md border border-line bg-surface text-xl font-bold text-ink disabled:cursor-not-allowed disabled:opacity-40'

function chipClass(active: boolean): string {
  return `min-h-12 rounded-md border px-4 text-sm font-semibold transition-colors ${
    active ? 'border-amber bg-amber/15 text-amber' : 'border-line bg-surface text-ink'
  }`
}

/**
 * Centered dialog for choosing a product's options (docs/pos-ux-spec.md §3a).
 * Mounted only while a product is being configured, so its state always starts
 * from the fast path: every required group pre-selected, quantity 1 — a cashier
 * who does not care just taps "Adisyona ekle" (two taps in total).
 *
 * A required group left empty does not disable the button: pressing it marks
 * the missing group and scrolls there instead (spec ilke 5 — say what is wrong
 * rather than hide the action). The mark is warn-colored, not red: red is
 * reserved for void/cancel.
 */
export function OptionPicker({ product, onConfirm, onCancel }: OptionPickerProps) {
  const groups = product.modifier_groups
  const [selection, setSelection] = useState<OptionSelection>(() => defaultSelection(groups))
  const [quantity, setQuantity] = useState(1)
  const [noteChips, setNoteChips] = useState<string[]>([])
  const [customNote, setCustomNote] = useState('')
  const [customNoteShown, setCustomNoteShown] = useState(false)
  const [showUnmet, setShowUnmet] = useState(false)
  const [blockedGroupId, setBlockedGroupId] = useState<string | null>(null)
  const groupRefs = useRef(new Map<string, HTMLElement>())

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') onCancel()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onCancel])

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
      groupRefs.current.get(unmet[0])?.scrollIntoView?.({ block: 'nearest' })
      return
    }
    onConfirm({ modifiers: chosen, note: composeFreeNote(noteChips, customNote), quantity })
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="option-picker-title"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
    >
      <div className="flex max-h-[90vh] w-full max-w-2xl flex-col overflow-hidden rounded-lg border border-line bg-panel">
        <div className="flex shrink-0 items-center justify-between gap-3 border-b border-line px-4 py-3">
          <h2 id="option-picker-title" className="font-display text-xl font-bold text-ink">
            {product.name}
          </h2>
          <div className="flex items-center gap-2">
            <span className="money font-display text-lg font-bold tabular-nums text-amber">
              {formatMoney(product.price_amount)}
            </span>
            <button type="button" onClick={onCancel} aria-label="Kapat" className="min-h-12 min-w-12 rounded text-ink-dim">
              ✕
            </button>
          </div>
        </div>

        <div className="flex-1 space-y-4 overflow-y-auto p-4">
          {groups.map((group) => {
            const isUnmet = showUnmet && unmet.includes(group.id)
            const picked = selection[group.id] ?? []
            return (
              <section
                key={group.id}
                ref={(el) => {
                  if (el) groupRefs.current.set(group.id, el)
                  else groupRefs.current.delete(group.id)
                }}
                aria-labelledby={`group-${group.id}`}
                className={`rounded-md border p-3 ${isUnmet ? 'border-warn' : 'border-transparent'}`}
              >
                <h3 id={`group-${group.id}`} className="mb-2 font-semibold text-ink">
                  {group.name} <span className="text-xs font-normal text-ink-dim">({groupHint(group)})</span>
                </h3>
                <div className="flex flex-wrap gap-2">
                  {group.modifiers.map((modifier) => {
                    const active = picked.includes(modifier.id)
                    const delta = formatDelta(modifier.price_delta)
                    return (
                      <button
                        key={modifier.id}
                        type="button"
                        aria-pressed={active}
                        onClick={() => handleToggle(group, modifier.id)}
                        className={chipClass(active)}
                      >
                        {active && <span aria-hidden="true">• </span>}
                        {modifier.name}
                        {delta && <span className="ml-1 tabular-nums text-ink-dim">{delta}</span>}
                      </button>
                    )
                  })}
                </div>
                {isUnmet && (
                  <p role="alert" className="mt-2 text-sm font-semibold text-warn">
                    Bu grupta bir seçim yapın.
                  </p>
                )}
                {blockedGroupId === group.id && (
                  <p role="status" className="mt-2 text-sm text-ink-dim">
                    En fazla {group.max_selections} seçim yapılabilir.
                  </p>
                )}
              </section>
            )
          })}

          <section className="p-3" aria-labelledby="option-note-title">
            <h3 id="option-note-title" className="mb-2 font-semibold text-ink">
              Not
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
                  {chip}
                </button>
              ))}
              <button
                type="button"
                aria-pressed={customNoteShown}
                onClick={() => setCustomNoteShown((shown) => !shown)}
                className={`min-h-12 rounded-md border px-4 text-sm ${
                  customNoteShown ? 'border-amber text-amber' : 'border-dashed border-line text-ink-dim'
                }`}
              >
                ✎ Not yaz
              </button>
            </div>
            {customNoteShown && (
              <input
                type="text"
                value={customNote}
                onChange={(e) => setCustomNote(e.target.value)}
                aria-label="Not"
                placeholder="Not yazın"
                className="mt-2 min-h-14 w-full rounded-md border border-line bg-surface px-3 text-ink"
              />
            )}
          </section>
        </div>

        <div className="shrink-0 space-y-3 border-t border-line p-4">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2">
              <span className="text-sm text-ink-dim">Adet</span>
              <button
                type="button"
                disabled={quantity <= 1}
                aria-label="Adedi azalt"
                onClick={() => setQuantity((q) => Math.max(1, q - 1))}
                className={STEP_BUTTON_CLASS}
              >
                −
              </button>
              <span className="qty min-w-8 text-center text-lg font-semibold text-ink" aria-live="polite">
                {quantity}
              </span>
              <button
                type="button"
                disabled={quantity >= MAX_LINE_QUANTITY}
                aria-label="Adedi artır"
                onClick={() => setQuantity((q) => Math.min(MAX_LINE_QUANTITY, q + 1))}
                className={STEP_BUTTON_CLASS}
              >
                +
              </button>
            </div>
            <div className="text-right">
              <span className="text-sm text-ink-dim">Satır </span>
              <span className="money font-display text-2xl font-bold tabular-nums text-ink">{formatMoney(lineTotal)}</span>
            </div>
          </div>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={onCancel}
              className="min-h-14 flex-1 rounded-lg border border-line font-semibold text-ink"
            >
              Vazgeç
            </button>
            <button
              type="button"
              autoFocus
              onClick={handleConfirm}
              className="min-h-14 flex-[2] rounded-lg bg-amber font-display text-lg font-bold text-amber-ink"
            >
              Adisyona ekle
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
