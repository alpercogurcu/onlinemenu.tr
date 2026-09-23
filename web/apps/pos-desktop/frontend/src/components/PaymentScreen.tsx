import { useEffect, useState } from 'react'
import {
  buildPaymentLines,
  cashChange,
  cashReceived,
  dueFor,
  formatMoney,
  formatMoneyInputDisplay,
  itemTotal,
  kurusToMoneyInput,
  parseMoneyInputToKurus,
  selectionTotal,
  unpaidItems,
  type DueMode,
  type PayMethod,
  type PayableItem,
  type PaymentLine,
} from '@onlinemenu/pos-core'
import { ErrorBanner } from './ErrorBanner'
import { CheckIcon } from './icons'
import { Numpad } from './Numpad'

// ₺50 / ₺100 / ₺200 / ₺500 in kuruş
const QUICK_NOTES = [5000, 10000, 20000, 50000]
const SPLIT_OPTIONS: { parts: number; label: string }[] = [
  // Turkish vowel harmony makes the dative suffix on a numeral irregular
  // (2 -> "2'ye", 3/4 -> "3'e"/"4'e") — spelled out, not templated.
  { parts: 2, label: "2'ye böl" },
  { parts: 3, label: "3'e böl" },
  { parts: 4, label: "4'e böl" },
]

/** What the parent registers: one payment and the basket it covers. */
export type PaymentRequest = {
  method: PayMethod
  /** What this payment settles (never more than what is left). */
  amount: number
  /** Cash physically handed over (equals `amount` for a card). */
  received: number
  lines: PaymentLine[]
  /** Order items an item payment covers; empty for full/split/amount payments. */
  itemIds: string[]
}

export type PaymentInitial = { due: number; method: PayMethod }

type PaymentScreenProps = {
  tableLabel: string
  items: readonly PayableItem[]
  paidItemIds: ReadonlySet<string>
  confirmedTotal: number
  settledPaidTotal: number
  /** What the customer still owes and the cashier may still collect. */
  remaining: number
  /** Set when the screen opens to retry a failed payment: starts on that amount. */
  initial: PaymentInitial | null
  onRegister: (request: PaymentRequest) => Promise<void>
  /** Back to the product grid. */
  onClose: () => void
  errorMessage: string
}

type NumpadTarget = 'received' | 'due'

const MODE_BUTTON = 'min-h-12 rounded-md border px-3 text-sm font-semibold'

function modeButtonClass(active: boolean, dashed = false): string {
  if (active) return `${MODE_BUTTON} border-amber bg-amber/15 text-amber`
  return `${MODE_BUTTON} ${dashed ? 'border-dashed text-ink-dim' : 'text-ink'} border-line bg-surface`
}

/**
 * Full-width payment screen that takes over the middle panel while a check is
 * being paid (docs/pos-ux-spec.md §3b): the 384px receipt rail cannot hold a
 * 56px numpad next to the amounts, so the rail stays as the check breakdown and
 * this screen does the taking.
 *
 * One payment settles a "due" amount — all that is left, an equal share, the
 * items the cashier ticked, or a typed amount — by cash or card. The default
 * (everything, cash, exact) is two taps: "Ödeme al" then "Nakit alındı".
 *
 * Whatever the due amount is, the payment goes out with fiscal lines that add up
 * to it (@onlinemenu/pos-core's paymentLines.ts): a payment that covers only some of the items must
 * not send them all, or a real ÖKC rejects the basket.
 */
export function PaymentScreen({
  tableLabel,
  items,
  paidItemIds,
  confirmedTotal,
  settledPaidTotal,
  remaining,
  initial,
  onRegister,
  onClose,
  errorMessage,
}: PaymentScreenProps) {
  const [method, setMethod] = useState<PayMethod>(initial?.method ?? 'cash')
  const [mode, setMode] = useState<DueMode>(initial ? 'custom' : 'full')
  const [splitParts, setSplitParts] = useState(2)
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())
  const [customDueInput, setCustomDueInput] = useState(initial ? kurusToMoneyInput(initial.due) : '')
  const [receivedInput, setReceivedInput] = useState('')
  const [target, setTarget] = useState<NumpadTarget>(initial ? 'due' : 'received')
  // A preset (₺100, a retried amount) fills a field; the next key then starts a
  // fresh amount instead of appending to it (see NumpadOptions.pendingReplace).
  const [pendingReplace, setPendingReplace] = useState(Boolean(initial))
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (remaining <= 0) onClose()
  }, [remaining, onClose])

  const unpaid = unpaidItems(items, paidItemIds)
  const selectedTotal = selectionTotal(unpaid, selected)
  const due = dueFor({
    mode,
    remaining,
    splitParts,
    selectedTotal,
    customDue: parseMoneyInputToKurus(customDueInput),
  })
  const received = method === 'cash' ? cashReceived(receivedInput, due) : due
  const change = cashChange(method, received, due)
  const shortOfCash = method === 'cash' && received < due
  const canSubmit = due > 0 && !shortOfCash && !submitting

  function chooseMode(next: DueMode) {
    setMode(next)
    setSelected(new Set())
    setReceivedInput('')
    setPendingReplace(false)
    setTarget('received')
  }

  function toggleItem(id: string) {
    setMode('items')
    setReceivedInput('')
    setTarget('received')
    setSelected((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function editDue() {
    if (mode !== 'custom') setCustomDueInput(kurusToMoneyInput(due))
    setMode('custom')
    setSelected(new Set())
    setTarget('due')
    setPendingReplace(true)
  }

  function chooseMethod(next: PayMethod) {
    setMethod(next)
    setReceivedInput('')
  }

  function applyReceivedPreset(kurus: number) {
    setTarget('received')
    setReceivedInput(kurusToMoneyInput(kurus))
    setPendingReplace(true)
  }

  function handleNumpad(next: string) {
    if (target === 'due') setCustomDueInput(next)
    else setReceivedInput(next)
    setPendingReplace(false)
  }

  async function handleSubmit() {
    const covered = mode === 'items' ? unpaid.filter((item) => selected.has(item.id)) : unpaid.length > 0 ? unpaid : [...items]
    const request: PaymentRequest = {
      method,
      amount: due,
      received,
      lines: buildPaymentLines(covered, due),
      itemIds: mode === 'items' ? covered.map((item) => item.id) : [],
    }
    setSubmitting(true)
    try {
      await onRegister(request)
    } catch {
      // The parent already surfaces the error (errorMessage) and re-syncs the
      // balance; the screen stays as it was so the cashier can retry.
      setSubmitting(false)
      return
    }
    setSubmitting(false)
    setReceivedInput('')
    setPendingReplace(false)
    setSelected(new Set())
    if (due >= remaining) {
      onClose()
      return
    }
    if (mode === 'split') {
      // splitParts counts the people still to pay, so the next share divides
      // what is left among them (an unchanged 3 would take a third of ⅔).
      if (splitParts <= 2) chooseMode('full')
      else setSplitParts(splitParts - 1)
    }
    if (mode === 'custom') chooseMode('full')
  }

  const numpadValue = target === 'due' ? customDueInput : receivedInput
  const showNumpad = target === 'due' || method === 'cash'

  return (
    <section className="flex h-full min-w-0 flex-1 flex-col overflow-hidden bg-surface" aria-label="Ödeme">
      <header className="flex shrink-0 items-center justify-between gap-3 border-b border-line px-4 py-2">
        <div className="min-w-0">
          <h2 className="truncate font-display text-lg font-bold text-ink">Ödeme — {tableLabel || 'Adisyon'}</h2>
          <p className="text-xs text-ink-dim tabular-nums">
            Toplam {formatMoney(confirmedTotal)} · Ödenen {formatMoney(settledPaidTotal)}
          </p>
        </div>
        <div className="flex items-center gap-3">
          <div className="text-right">
            <p className="text-xs uppercase tracking-wide text-ink-dim">Kalan</p>
            <p className="money font-display text-3xl font-bold leading-none tabular-nums text-ink">
              {formatMoney(remaining)}
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="min-h-12 rounded-md border border-line px-4 font-semibold text-ink"
          >
            ← Ürünlere dön
          </button>
        </div>
      </header>

      <div className="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_21rem] gap-4 p-4">
        <div className="flex min-h-0 flex-col gap-3">
          <p className="text-sm text-ink-dim">Ne kadar ödenecek?</p>
          <div className="grid grid-cols-4 gap-2">
            <button type="button" aria-pressed={mode === 'full'} onClick={() => chooseMode('full')} className={modeButtonClass(mode === 'full')}>
              Tümü
            </button>
            {SPLIT_OPTIONS.map(({ parts, label }) => (
              <button
                key={parts}
                type="button"
                aria-pressed={mode === 'split' && splitParts === parts}
                onClick={() => {
                  chooseMode('split')
                  setSplitParts(parts)
                }}
                className={modeButtonClass(mode === 'split' && splitParts === parts)}
              >
                {label}
              </button>
            ))}
          </div>
          <div className="grid grid-cols-2 gap-2">
            <button type="button" aria-pressed={mode === 'items'} onClick={() => chooseMode('items')} className={modeButtonClass(mode === 'items')}>
              Kalem seç
            </button>
            <button type="button" aria-pressed={mode === 'custom'} onClick={editDue} className={modeButtonClass(mode === 'custom', true)}>
              ✎ Başka tutar
            </button>
          </div>

          {mode === 'items' ? (
            <ItemPicker
              unpaid={unpaid}
              paidItemIds={paidItemIds}
              allItems={items}
              selected={selected}
              selectedTotal={selectedTotal}
              remaining={remaining}
              onToggle={toggleItem}
              onSelectAll={() => {
                setSelected(new Set(unpaid.map((item) => item.id)))
                setReceivedInput('')
              }}
              onClear={() => setSelected(new Set())}
            />
          ) : (
            <p className="rounded-md border border-dashed border-line p-4 text-sm text-ink-dim">
              {mode === 'full' && 'Kalanın tamamı tek ödemede alınır.'}
              {mode === 'split' &&
                `${splitParts} kişi kaldı — kişi başı ${formatMoney(due)}. Her ödemeden sonra kalan kişilere bölünür.`}
              {mode === 'custom' && 'Sağdaki tuş takımıyla ödenecek tutarı yazın.'}
            </p>
          )}
        </div>

        <div className="flex min-h-0 flex-col gap-2 overflow-y-auto">
          <button
            type="button"
            aria-pressed={target === 'due'}
            onClick={editDue}
            className={`flex min-h-14 items-baseline justify-between gap-2 rounded-md border bg-panel px-3 py-2 ${
              target === 'due' ? 'border-amber' : 'border-line'
            }`}
          >
            <span className="text-sm text-ink-dim">Ödenecek</span>
            <span className="money font-display text-2xl font-bold tabular-nums text-ink">
              {target === 'due' && mode === 'custom' ? `${formatMoneyInputDisplay(customDueInput)} ₺` : formatMoney(due)}
            </span>
          </button>

          <div className="grid grid-cols-2 gap-2">
            <button type="button" aria-pressed={method === 'cash'} onClick={() => chooseMethod('cash')} className={modeButtonClass(method === 'cash')}>
              Nakit
            </button>
            <button type="button" aria-pressed={method === 'card'} onClick={() => chooseMethod('card')} className={modeButtonClass(method === 'card')}>
              Kart
            </button>
          </div>

          {method === 'cash' && (
            <button
              type="button"
              aria-pressed={target === 'received'}
              onClick={() => {
                setTarget('received')
                setPendingReplace(false)
              }}
              className={`flex min-h-14 items-baseline justify-between gap-2 rounded-md border bg-panel px-3 py-2 ${
                target === 'received' ? 'border-amber' : 'border-line'
              }`}
            >
              <span className="text-sm text-ink-dim">Alınan</span>
              {receivedInput.trim() === '' ? (
                <span className="money text-lg tabular-nums text-ink-dim">
                  {formatMoney(due)} <span className="text-xs">(tam)</span>
                </span>
              ) : (
                <span className="money text-2xl font-semibold tabular-nums text-ink">
                  {formatMoneyInputDisplay(receivedInput)} ₺
                </span>
              )}
            </button>
          )}

          {method === 'cash' && change > 0 && (
            <div className="flex items-baseline justify-between gap-2">
              <p className="text-ink-dim">Para üstü</p>
              <p key={change} className="money change-due-pulse font-display text-3xl font-bold tabular-nums text-teal">
                {formatMoney(change)}
              </p>
            </div>
          )}
          {shortOfCash && (
            <p className="text-sm text-ink" role="status">
              Alınan tutar ödenecek tutardan az — eksik {formatMoney(due - received)}.
            </p>
          )}

          {method === 'cash' && target === 'received' && (
            <div className="grid grid-cols-5 gap-2">
              {QUICK_NOTES.map((note) => (
                <button
                  key={note}
                  type="button"
                  onClick={() => applyReceivedPreset(note)}
                  className="min-h-12 rounded-md border border-line bg-surface text-xs font-semibold text-ink"
                >
                  ₺{note / 100}
                </button>
              ))}
              <button
                type="button"
                aria-label="Tam tutar"
                onClick={() => {
                  setReceivedInput('')
                  setPendingReplace(false)
                }}
                className="min-h-12 rounded-md border border-amber bg-surface text-xs font-semibold text-amber"
              >
                Tam
              </button>
            </div>
          )}

          {showNumpad ? (
            <Numpad
              mode="money"
              value={numpadValue}
              pendingReplace={pendingReplace}
              onChange={handleNumpad}
              disabled={submitting}
            />
          ) : (
            <p className="rounded-md border border-dashed border-line p-3 text-sm text-ink-dim">
              Kart ödemesi harici POS cihazından çekilir; burada yalnızca kaydı tutulur.
            </p>
          )}

          <ErrorBanner message={errorMessage} />
          <button
            type="button"
            disabled={!canSubmit}
            onClick={handleSubmit}
            className="min-h-14 w-full shrink-0 rounded-lg bg-amber px-4 font-display text-lg font-bold text-amber-ink disabled:opacity-40"
          >
            {submitting
              ? 'Kaydediliyor…'
              : method === 'cash'
                ? 'Nakit alındı'
                : 'Kartla çekildi (harici cihaz)'}
          </button>
        </div>
      </div>
    </section>
  )
}

type ItemPickerProps = {
  unpaid: readonly PayableItem[]
  paidItemIds: ReadonlySet<string>
  allItems: readonly PayableItem[]
  selected: ReadonlySet<string>
  selectedTotal: number
  remaining: number
  onToggle: (id: string) => void
  onSelectAll: () => void
  onClear: () => void
}

/**
 * The items of the check as tickable rows. Paid items stay listed (dimmed, with
 * a check) rather than vanishing, so the cashier can see what is already
 * settled; the "kalan" shown is the money still owed after the ticked items,
 * which is what actually decides whether the check can be closed.
 */
function ItemPicker({
  unpaid,
  paidItemIds,
  allItems,
  selected,
  selectedTotal,
  remaining,
  onToggle,
  onSelectAll,
  onClear,
}: ItemPickerProps) {
  const paid = allItems.filter((item) => paidItemIds.has(item.id))
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      <div className="flex items-center justify-between gap-2">
        <p className="text-sm tabular-nums text-ink">
          Seçilen <span className="money font-semibold">{formatMoney(selectedTotal)}</span> · Kalan{' '}
          <span className="money font-semibold">{formatMoney(Math.max(0, remaining - selectedTotal))}</span>
        </p>
        <div className="flex gap-2">
          <button type="button" onClick={onSelectAll} className="min-h-12 rounded-md border border-line px-3 text-sm font-semibold text-ink">
            Hepsini seç
          </button>
          <button type="button" onClick={onClear} className="min-h-12 rounded-md border border-line px-3 text-sm text-ink-dim">
            Temizle
          </button>
        </div>
      </div>
      <ul className="min-h-0 flex-1 space-y-2 overflow-y-auto">
        {unpaid.map((item) => {
          const isSelected = selected.has(item.id)
          return (
            <li key={item.id}>
              <button
                type="button"
                aria-pressed={isSelected}
                onClick={() => onToggle(item.id)}
                className={`flex min-h-14 w-full items-center gap-3 rounded-md border px-3 py-2 text-left ${
                  isSelected ? 'border-amber bg-amber/10' : 'border-line bg-panel'
                }`}
              >
                <span
                  aria-hidden="true"
                  className={`flex h-6 w-6 shrink-0 items-center justify-center rounded border ${
                    isSelected ? 'border-amber bg-amber text-amber-ink' : 'border-ink-dim'
                  }`}
                >
                  {isSelected && <CheckIcon size={16} />}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block text-ink">
                    {item.quantity}× {item.name}
                  </span>
                  {item.note && <span className="block break-words text-xs text-ink-dim">{item.note}</span>}
                </span>
                <span className="money tabular-nums text-ink">{formatMoney(itemTotal(item))}</span>
              </button>
            </li>
          )
        })}
        {paid.map((item) => (
          <li
            key={item.id}
            className="flex min-h-12 items-center gap-3 rounded-md border border-line/50 px-3 py-2 text-ink-dim"
          >
            <CheckIcon size={16} className="shrink-0 text-teal" />
            <span className="min-w-0 flex-1 truncate">
              {item.quantity}× {item.name}
            </span>
            <span className="text-xs text-teal">Ödendi</span>
            <span className="money tabular-nums">{formatMoney(itemTotal(item))}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}
