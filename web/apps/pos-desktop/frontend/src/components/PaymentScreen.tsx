import { useEffect, useMemo, useState } from 'react'
import {
  buildPaymentLines,
  cashChange,
  cashReceived,
  coveredItems,
  dueFor,
  formatMoney,
  formatMoneyInputDisplay,
  kurusToMoneyInput,
  parseMoneyInputToKurus,
  selectedUnitCount,
  selectionAllocations,
  selectionTotal,
  type DueMode,
  type ItemQty,
  type PaidQty,
  type PayMethod,
  type PayableItem,
  type PaySelection,
  type PaymentLine,
} from '@onlinemenu/pos-core'
import { seatChip, seatGroups, seatLabel, seatSelected, toggleSeat } from '../lib/seatTotals'
import { ErrorBanner } from './ErrorBanner'
import { CheckIcon } from './icons'
import { Numpad } from './Numpad'

// ₺50 / ₺100 / ₺200 / ₺500 in kuruş
const QUICK_NOTES = [5000, 10000, 20000, 50000]
/** Under the seat chips — what tapping one does. */
export const SEAT_TAP_HINT = 'Kişiye dokun — o kişinin ödenmemiş kalemleri seçilir. Birden çok kişi birlikte seçilebilir.'
/** Shown while nothing is picked: the receipt rail is the selection surface. */
export const SELECT_FROM_RECEIPT_HINT = 'Sağdaki adisyondan kalem seçebilirsiniz.'
/** Permanent line under the Kart action: no ÖKC/card device is integrated
 * until Faz 2, so a card payment here is only a bookkeeping record. Static on
 * purpose — there is no device state to read yet. */
export const CARD_DEVICE_NOTICE =
  'Kart: cihaz kayıtlı değil — tahsilat harici cihazda yapılır, buraya yalnız kayıt düşer.'

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
  /** Units an item payment covers; empty for full/split/amount payments. */
  items: ItemQty[]
}

export type PaymentInitial = { due: number; method: PayMethod }

type PaymentScreenProps = {
  tableLabel: string
  items: readonly PayableItem[]
  paidQty: PaidQty
  /** Units picked on the receipt rail; owned by App because the rail is a
   * sibling of this screen. */
  selection: PaySelection
  onSelectionChange: (update: (current: PaySelection) => PaySelection) => void
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
 * units the cashier picked on the receipt rail, or a typed amount — by cash or
 * card. Picking on the rail (or a guest chip) switches to the item mode; the
 * mode buttons drop the pick. The method is
 * the action itself ("Nakit Al" primary, "Kart" secondary — approved payment
 * design): the default (everything, cash, exact) stays two taps, "Ödeme al"
 * then "Nakit Al".
 *
 * Whatever the due amount is, the payment goes out with fiscal lines that add up
 * to it (@onlinemenu/pos-core's paymentLines.ts): a payment that covers only some of the items must
 * not send them all, or a real ÖKC rejects the basket.
 */
export function PaymentScreen({
  tableLabel,
  items,
  paidQty,
  selection,
  onSelectionChange,
  confirmedTotal,
  settledPaidTotal,
  remaining,
  initial,
  onRegister,
  onClose,
  errorMessage,
}: PaymentScreenProps) {
  const [mode, setMode] = useState<DueMode>(initial ? 'custom' : 'full')
  const [splitParts, setSplitParts] = useState(2)
  const [customDueInput, setCustomDueInput] = useState(initial ? kurusToMoneyInput(initial.due) : '')
  const [receivedInput, setReceivedInput] = useState('')
  const [target, setTarget] = useState<NumpadTarget>(initial ? 'due' : 'received')
  // A preset (₺100, a retried amount) fills a field; the next key then starts a
  // fresh amount instead of appending to it (see NumpadOptions.pendingReplace).
  const [pendingReplace, setPendingReplace] = useState(Boolean(initial))
  const [submittingMethod, setSubmittingMethod] = useState<PayMethod | null>(null)
  const submitting = submittingMethod !== null

  // A pick on the rail lands here as a new selection: adjusting state during
  // render (not in an effect) keeps the mode switch and the "Alınan" reset in
  // the same paint as the new amount.
  const [seenSelection, setSeenSelection] = useState(selection)
  if (selection !== seenSelection) {
    setSeenSelection(selection)
    setReceivedInput('')
    if (selection.size > 0) {
      setMode('items')
      setTarget('received')
      setPendingReplace(false)
    }
  }

  useEffect(() => {
    if (remaining <= 0) onClose()
  }, [remaining, onClose])

  const selectedTotal = selectionTotal(items, paidQty, selection)
  const selectedCount = selectedUnitCount(items, paidQty, selection)
  const guests = useMemo(() => seatGroups(items, paidQty), [items, paidQty])
  const due = dueFor({
    mode,
    remaining,
    splitParts,
    selectedTotal,
    customDue: parseMoneyInputToKurus(customDueInput),
  })
  const received = cashReceived(receivedInput, due)
  const change = cashChange('cash', received, due)
  const shortOfCash = received < due
  const canPayCard = due > 0 && !submitting
  const canPayCash = canPayCard && !shortOfCash

  function clearSelection() {
    if (selection.size > 0) onSelectionChange(() => new Map())
  }

  function chooseMode(next: DueMode) {
    setMode(next)
    clearSelection()
    setReceivedInput('')
    setPendingReplace(false)
    setTarget('received')
  }

  function editDue() {
    if (mode !== 'custom') setCustomDueInput(kurusToMoneyInput(due))
    setMode('custom')
    clearSelection()
    setTarget('due')
    setPendingReplace(true)
  }

  // Alman usulü shortcut: a guest chip picks (or releases) that guest's unpaid
  // units on the rail, where the cashier can still trim them. Nothing
  // seat-specific is sent to the backend.
  function toggleGuest(seat: number) {
    onSelectionChange((current) => toggleSeat(items, paidQty, current, seat))
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

  async function handleSubmit(payMethod: PayMethod) {
    const request: PaymentRequest = {
      method: payMethod,
      amount: due,
      received: payMethod === 'cash' ? received : due,
      lines: buildPaymentLines(coveredItems(mode, items, paidQty, selection), due),
      items: mode === 'items' ? selectionAllocations(items, paidQty, selection) : [],
    }
    setSubmittingMethod(payMethod)
    try {
      await onRegister(request)
    } catch {
      // The parent already surfaces the error (errorMessage) and re-syncs the
      // balance; the screen stays as it was so the cashier can retry.
      setSubmittingMethod(null)
      return
    }
    setSubmittingMethod(null)
    setReceivedInput('')
    setPendingReplace(false)
    clearSelection()
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

      <div className="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_24rem] gap-4 p-4">
        <div className="flex min-h-0 flex-col gap-3 overflow-y-auto">
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
            <button type="button" aria-pressed={mode === 'custom'} onClick={editDue} className={modeButtonClass(mode === 'custom', true)}>
              ✎ Başka tutar
            </button>
            <button
              type="button"
              disabled={selection.size === 0}
              onClick={clearSelection}
              className="min-h-12 rounded-md border border-line bg-surface px-3 text-sm font-semibold text-ink disabled:opacity-40"
            >
              Seçimi temizle
            </button>
          </div>

          {guests.length > 0 && (
            <div>
              <p className="text-xs uppercase tracking-wide text-ink-dim">Kişiler</p>
              <div className="mt-1 flex flex-wrap gap-2">
                {guests.map((group) => {
                  const chip = seatChip(group)
                  const picked = !chip.settled && seatSelected(items, paidQty, selection, group.seat)
                  return (
                    <button
                      key={group.seat}
                      type="button"
                      disabled={chip.settled}
                      aria-pressed={picked}
                      onClick={() => toggleGuest(group.seat)}
                      aria-label={
                        chip.settled
                          ? `${seatLabel(group.seat)} ödendi`
                          : picked
                            ? `${seatLabel(group.seat)} seçili — bırak`
                            : `${seatLabel(group.seat)} — kalan ${formatMoney(group.remaining)} kalemlerini seç`
                      }
                      className={`flex min-h-12 items-center gap-1.5 rounded-lg px-3.5 text-sm font-semibold ${
                        chip.settled
                          ? 'border border-teal/40 bg-teal/15 text-teal'
                          : picked
                            ? 'border border-amber bg-amber text-amber-ink'
                            : 'border border-line bg-panel text-ink'
                      }`}
                    >
                      {(chip.settled || picked) && <CheckIcon size={14} />}
                      <span className="money tabular-nums">{chip.label}</span>
                    </button>
                  )
                })}
              </div>
              <p className="mt-1.5 text-xs text-ink-dim">{SEAT_TAP_HINT}</p>
            </div>
          )}

          <div className="rounded-md border border-line bg-panel px-4 py-3" role="status">
            <p className="text-base tabular-nums text-ink">
              Seçilen <span className="money font-semibold">{formatMoney(due)}</span>
              {' → '}bu ödemeden sonra kalan{' '}
              <span className="money font-semibold">{formatMoney(Math.max(0, remaining - due))}</span>
            </p>
            <p className="mt-1 text-sm text-ink-dim">
              {mode === 'full' && 'Kalanın tamamı tek ödemede alınır.'}
              {mode === 'split' &&
                `${splitParts} kişi kaldı — kişi başı ${formatMoney(due)}. Her ödemeden sonra kalan kişilere bölünür.`}
              {mode === 'custom' && 'Sağdaki tuş takımıyla ödenecek tutarı yazın.'}
              {mode === 'items' &&
                (selectedCount > 0
                  ? `${selectedCount} birim seçildi — fişe gerçek adet ve fiyatla basılır.`
                  : 'Henüz kalem seçilmedi.')}
              {mode === 'items' && selectedTotal > remaining && ' Seçim kalan borçtan fazla — kalan kadarı alınır.'}
            </p>
          </div>
          {selectedCount === 0 && (
            <p className="rounded-md border border-dashed border-line p-4 text-sm text-ink-dim">{SELECT_FROM_RECEIPT_HINT}</p>
          )}
        </div>

        <div className="flex min-h-0 flex-col gap-2 overflow-y-auto">
          <button
            type="button"
            aria-pressed={target === 'due'}
            onClick={editDue}
            className={`flex min-h-20 shrink-0 items-baseline justify-between gap-3 rounded-lg border bg-panel px-4 py-3 ${
              target === 'due' ? 'border-amber' : 'border-line'
            }`}
          >
            <span className="text-sm text-ink-dim">Ödenecek</span>
            <span className="money font-display text-4xl font-bold tabular-nums text-ink">
              {target === 'due' && mode === 'custom' ? `${formatMoneyInputDisplay(customDueInput)} ₺` : formatMoney(due)}
            </span>
          </button>

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

          {change > 0 && (
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

          {target === 'received' && (
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

          <Numpad
            mode="money"
            value={numpadValue}
            pendingReplace={pendingReplace}
            onChange={handleNumpad}
            disabled={submitting}
          />

          <ErrorBanner message={errorMessage} />
          <div className="grid shrink-0 grid-cols-2 gap-2">
            <button
              type="button"
              disabled={!canPayCash}
              onClick={() => handleSubmit('cash')}
              className="min-h-16 rounded-lg bg-amber px-4 font-display text-lg font-bold text-amber-ink disabled:opacity-40"
            >
              {submittingMethod === 'cash' ? 'Kaydediliyor…' : 'Nakit Al'}
            </button>
            <button
              type="button"
              disabled={!canPayCard}
              onClick={() => handleSubmit('card')}
              className="min-h-16 rounded-lg border border-line bg-panel px-4 font-display text-lg font-bold text-ink disabled:opacity-40"
            >
              {submittingMethod === 'card' ? 'Kaydediliyor…' : 'Kart'}
            </button>
          </div>
          <p className="shrink-0 text-xs text-warn">{CARD_DEVICE_NOTICE}</p>
        </div>
      </div>
    </section>
  )
}
