import { useState } from 'react'
import type { main } from '../../wailsjs/go/models'
import type { PendingLine } from '../lib/cart'
import { pendingLineTotal } from '../lib/cart'
import type { TrackedPayment } from '../lib/fiscalStatus'
import { formatMoney, parseMoneyInputToKurus } from '../lib/format'
import { changeDue as computeChangeDue, clampToRemaining, splitSuggestion } from '../lib/payment'
import { ErrorBanner } from './ErrorBanner'
import { FiscalStatusBadge } from './FiscalStatusBadge'
import { HoldButton } from './HoldButton'

const QUICK_NOTES = [5000, 10000, 20000, 50000] // ₺50 / ₺100 / ₺200 / ₺500 in kuruş
// Turkish vowel harmony makes the dative suffix on a numeral irregular
// across values (2 -> "2'ye", 3/4 -> "3'e"/"4'e") — not templatable from
// the number alone, so each label is spelled out rather than built from
// `${parts}'e böl`.
const SPLIT_PARTS: { parts: number; label: string }[] = [
  { parts: 2, label: "2'ye böl" },
  { parts: 3, label: "3'e böl" },
  { parts: 4, label: "4'e böl" },
]

/** Kuruş -> the "1234,56" shape the amount input expects. */
function toMoneyInput(kurus: number): string {
  return (kurus / 100).toFixed(2).replace('.', ',')
}

type ReceiptProps = {
  tableLabel: string
  confirmedOrders: main.OrderDTO[]
  pendingLines: PendingLine[]
  onRemovePendingLine: (clientId: string) => void
  onSendOrder: () => Promise<void>
  sendingOrder: boolean
  confirmedTotal: number
  pendingTotal: number
  /** Money whose fiscal record has SETTLED (server-recorded completed payments
   * plus this session's completed ones). Only this enables the close. */
  settledPaidTotal: number
  /** What the cashier may still collect: total − settled − pending-in-flight.
   * Computed in App (see lib/fiscalStatus.collectableRemaining) because it now
   * needs the full payment list, not a single accumulated number. */
  remaining: number
  /** Settled money covers the check — mirrors the backend's own paid-in-full
   * guard. Distinct from "closable": see closeBlockReason. */
  isFullyPaid: boolean
  /** Non-null while any payment on this check is awaiting a fiscal record
   * (requirement 4). Blocks the close even when isFullyPaid is true. */
  closeBlockReason: string | null
  /** Payments registered against this check in this session, with live fiscal
   * status. Empty for a check whose payments all predate this session. */
  payments: readonly TrackedPayment[]
  /**
   * amountToRegister is the CLAMPED amount for this one cash-payment step
   * (never more than the remaining balance — see lib/payment's
   * clampToRemaining). receivedAmount is the raw cash the customer physically
   * handed over for this step (which may exceed amountToRegister when change is
   * due) — passed alongside so the parent can attach it to the tracked payment
   * for the receipt print that follows CloseCheck.
   */
  onRegisterPayment: (amountToRegister: number, receivedAmount: number) => Promise<void>
  /** Requirement 3 — drop a failed payment from the tracked list so its amount
   * returns to the collectable balance before the retry re-registers it. */
  onDiscardFailedPayment: (paymentId: string) => void
  onCloseCheck: () => Promise<void>
  errorMessage: string
}

/**
 * Right rail — the signature element: a live thermal-receipt view of the
 * current adisyon. Confirmed (already sent to kitchen) lines are read-only
 * mono rows; unsent lines are the same style but removable (void, red —
 * the only place red appears outside close/cancel). Cash payment mode
 * expands this panel in place instead of opening a modal.
 *
 * Since ADR-FISCAL-002 a registered payment is not the end of the story: each
 * one carries a fiscal-record status badge until the ÖKC confirms the receipt.
 */
export function Receipt({
  tableLabel,
  confirmedOrders,
  pendingLines,
  onRemovePendingLine,
  onSendOrder,
  sendingOrder,
  confirmedTotal,
  pendingTotal,
  settledPaidTotal,
  remaining,
  isFullyPaid,
  closeBlockReason,
  payments,
  onRegisterPayment,
  onDiscardFailedPayment,
  onCloseCheck,
  errorMessage,
}: ReceiptProps) {
  const [cashMode, setCashMode] = useState(false)
  const [receivedInput, setReceivedInput] = useState('')
  const [submittingPayment, setSubmittingPayment] = useState(false)

  const grandTotal = confirmedTotal + pendingTotal
  const canSendOrder = pendingLines.length > 0 && !sendingOrder

  // `remaining` (not a sticky "hasPaid" flag) still drives everything, but it
  // is now computed by App from three inputs — settled money, pending-in-flight
  // money, and the check total — rather than from a single accumulated total.
  const canPay = pendingLines.length === 0 && confirmedTotal > 0 && remaining > 0

  // An empty amount field means "exact cash for the remaining balance, no
  // change" — the common case (including the common case of a single,
  // non-split payment, where remaining === confirmedTotal). The cashier
  // only types an amount when paying a partial share or when change is
  // due, so a blank field must not block "Nakit alındı".
  const receivedBlank = receivedInput.trim() === ''
  const receivedKurus = receivedBlank ? remaining : parseMoneyInputToKurus(receivedInput)
  // What actually gets registered as a payment — never more than what is
  // still owed, so a cashier entering more than remaining (to make change
  // on the final installment) never overpays the check; the excess comes
  // back as changeDue, not as a recorded payment.
  const amountToRegister = clampToRemaining(receivedKurus, remaining)
  const changeDue = computeChangeDue(receivedKurus, remaining)
  const receivedEnough = receivedKurus > 0 && remaining > 0

  async function handleConfirmPayment() {
    setSubmittingPayment(true)
    try {
      await onRegisterPayment(amountToRegister, receivedKurus)
      setReceivedInput('')
      // Cash mode only closes itself once the balance is fully settled —
      // otherwise it stays open, cleared, ready for the next installment.
      if (remaining - amountToRegister <= 0) {
        setCashMode(false)
      }
    } catch {
      // errorMessage is already derived from onRegisterPayment's own state
      // update in the parent (App), which also refreshes the remaining
      // balance from the server on failure — nothing further to do here
      // besides staying in cash mode so the cashier can retry.
    } finally {
      setSubmittingPayment(false)
    }
  }

  // Requirement 3 — "Yeniden dene": drop the failed payment (returning its
  // amount to `remaining`) and reopen the ordinary cash flow with that amount
  // prefilled. No bespoke retry endpoint: this registers a brand-new payment,
  // exactly as if the cashier had typed the amount again.
  function handleRetryPayment(payment: TrackedPayment) {
    onDiscardFailedPayment(payment.id)
    setReceivedInput(toMoneyInput(payment.amountTotal))
    setCashMode(true)
  }

  return (
    <aside className="flex h-full w-96 shrink-0 flex-col border-l border-line bg-panel">
      <div className="border-b border-line p-4">
        <h2 className="font-display text-lg font-bold text-ink">{tableLabel || 'Adisyon'}</h2>
      </div>

      {!cashMode && (
        <>
          <div className="flex-1 overflow-y-auto px-4 py-2 font-mono text-sm text-ink">
            {confirmedOrders.length === 0 && pendingLines.length === 0 && (
              <p className="py-6 text-center text-ink-dim">Adisyon boş — ürün ekleyin.</p>
            )}

            {confirmedOrders.map((order) =>
              order.items.map((item) => (
                <div key={item.id} className="receipt-line-enter flex justify-between gap-2 py-1">
                  <span className="qty text-ink-dim">{item.quantity}×</span>
                  <span className="flex-1 truncate">{item.product_name}</span>
                  <span className="money tabular-nums">
                    {formatMoney(item.quantity * item.unit_price_amount)}
                  </span>
                </div>
              )),
            )}

            {pendingLines.map((line) => (
              <div
                key={line.clientId}
                className="receipt-line-enter flex items-center justify-between gap-2 border-t border-dashed border-line/60 py-1 text-ink-dim"
              >
                <span className="qty">{line.quantity}×</span>
                <span className="flex-1 truncate">
                  {line.productName} <span className="text-xs">(gönderilmedi)</span>
                </span>
                <span className="money tabular-nums">{formatMoney(pendingLineTotal(line))}</span>
                <button
                  type="button"
                  aria-label={`${line.productName} satırını kaldır`}
                  onClick={() => onRemovePendingLine(line.clientId)}
                  className="ml-1 min-h-8 min-w-8 rounded text-danger"
                >
                  ×
                </button>
              </div>
            ))}
          </div>

          <div className="receipt-tear" aria-hidden="true" />

          <div className="space-y-3 p-4">
            <div className="flex items-baseline justify-between">
              <span className="text-ink-dim">Ara toplam</span>
              <span className="money font-display text-2xl font-bold tabular-nums text-ink">
                {formatMoney(grandTotal)}
              </span>
            </div>

            {settledPaidTotal > 0 && (
              <div className="flex items-baseline justify-between rounded-md bg-teal/10 px-2 py-1 text-sm">
                <span className="text-ink-dim">Önceden ödenen</span>
                <span className="money font-semibold tabular-nums text-teal">
                  {formatMoney(settledPaidTotal)}
                </span>
              </div>
            )}

            <PaymentStatusList payments={payments} onRetry={handleRetryPayment} />

            <ErrorBanner message={errorMessage} />

            {pendingLines.length > 0 && (
              <button
                type="button"
                onClick={onSendOrder}
                disabled={!canSendOrder}
                className="min-h-14 w-full rounded-lg bg-teal px-4 font-semibold text-amber-ink disabled:opacity-50"
              >
                {sendingOrder ? 'Gönderiliyor…' : 'Siparişi gönder'}
              </button>
            )}

            {!isFullyPaid && (
              <button
                type="button"
                disabled={!canPay}
                onClick={() => setCashMode(true)}
                className="min-h-14 w-full rounded-lg bg-amber px-4 font-display text-lg font-bold text-amber-ink disabled:opacity-40"
              >
                Nakit al
              </button>
            )}
            {!isFullyPaid && pendingLines.length > 0 && (
              <p className="text-center text-xs text-ink-dim">Önce siparişi gönderin</p>
            )}

            {/* Requirement 4 — the close is withheld, not merely disabled, while
                a fiscal record is outstanding: a disabled HoldButton would still
                invite the cashier to press and hold it for two seconds before
                learning nothing happens. The reason takes its place. */}
            {closeBlockReason ? (
              <p
                className="rounded-md border border-line bg-surface px-3 py-2 text-center text-sm text-ink"
                role="status"
              >
                {closeBlockReason} — mali kayıt tamamlanmadan adisyon kapatılamaz.
              </p>
            ) : (
              isFullyPaid && (
                <HoldButton label="Basılı tutup kapat" holdingLabel="Kapatılıyor…" onConfirm={onCloseCheck} />
              )
            )}
          </div>
        </>
      )}

      {cashMode && (
        <div className="flex flex-1 flex-col justify-between p-4">
          <div>
            <p className="text-ink-dim">Kalan</p>
            <p className="money font-display text-4xl font-bold tabular-nums text-ink">
              {formatMoney(remaining)}
            </p>
            {settledPaidTotal > 0 && (
              <p className="mt-1 text-xs text-ink-dim">
                {formatMoney(confirmedTotal)} hesaptan {formatMoney(settledPaidTotal)} ödendi
              </p>
            )}

            <div className="mt-4 grid grid-cols-3 gap-2">
              {QUICK_NOTES.map((note) => (
                <button
                  key={note}
                  type="button"
                  onClick={() => setReceivedInput(toMoneyInput(note))}
                  className="min-h-14 rounded-md border border-line bg-surface font-semibold text-ink"
                >
                  {formatMoney(note)}
                </button>
              ))}
              <button
                type="button"
                onClick={() => setReceivedInput('')}
                className="min-h-14 rounded-md border border-line bg-surface font-semibold text-ink"
              >
                Kalanın tamamı
              </button>
            </div>

            {/* Quick split — suggests an equal share of the REMAINING
                balance (not the full check), so splitting after a partial
                payment already made still divides what is actually left. */}
            <div className="mt-2 grid grid-cols-3 gap-2">
              {SPLIT_PARTS.map(({ parts, label }) => (
                <button
                  key={parts}
                  type="button"
                  onClick={() => setReceivedInput(toMoneyInput(splitSuggestion(remaining, parts)))}
                  className="min-h-10 rounded-md border border-dashed border-line bg-surface text-sm text-ink-dim"
                >
                  {label}
                </button>
              ))}
            </div>

            <label className="mt-4 block text-sm text-ink-dim" htmlFor="received-amount">
              Alınan tutar <span className="text-ink-dim/70">(boş = kalanın tamamı)</span>
            </label>
            <input
              id="received-amount"
              inputMode="decimal"
              placeholder={formatMoney(remaining)}
              className="money min-h-14 w-full rounded-md border border-line bg-surface px-3 py-2 text-xl tabular-nums text-ink placeholder:text-ink-dim/50"
              value={receivedInput}
              onChange={(e) => setReceivedInput(e.target.value)}
              autoFocus
            />

            {changeDue > 0 ? (
              <div className="mt-4">
                <p className="text-ink-dim">Para üstü</p>
                <p key={changeDue} className="money change-due-pulse font-display text-5xl font-bold tabular-nums text-teal">
                  {formatMoney(changeDue)}
                </p>
              </div>
            ) : (
              // Partial payment (entered < remaining) — no change is due,
              // so show what this installment leaves behind instead, per
              // req item 1 ("kalan her zaman görünür").
              receivedKurus > 0 &&
              amountToRegister < remaining && (
                <div className="mt-4">
                  <p className="text-ink-dim">Bu ödemeden sonra kalan</p>
                  <p className="money font-display text-3xl font-bold tabular-nums text-ink-dim">
                    {formatMoney(remaining - amountToRegister)}
                  </p>
                </div>
              )
            )}
          </div>

          <div className="space-y-2">
            <ErrorBanner message={errorMessage} />
            <button
              type="button"
              disabled={!receivedEnough || submittingPayment}
              onClick={handleConfirmPayment}
              className="min-h-14 w-full rounded-lg bg-amber px-4 font-display text-lg font-bold text-amber-ink disabled:opacity-40"
            >
              {submittingPayment ? 'Kaydediliyor…' : 'Nakit alındı'}
            </button>
            <button
              type="button"
              onClick={() => setCashMode(false)}
              className="min-h-14 w-full rounded-lg border border-line px-4 font-medium text-ink-dim"
            >
              Vazgeç
            </button>
          </div>
        </div>
      )}
    </aside>
  )
}

/**
 * One row per payment this session registered against the check, each carrying
 * its fiscal-record status (requirement 1). A payment whose registration failed
 * additionally shows the reason and a full-width retry target (requirement 3 —
 * min-h-11 = 44px).
 *
 * Nothing renders for a check with no payments yet, so the ordinary
 * add-items-and-send flow is visually untouched.
 */
function PaymentStatusList({
  payments,
  onRetry,
}: {
  payments: readonly TrackedPayment[]
  onRetry: (payment: TrackedPayment) => void
}) {
  if (payments.length === 0) return null

  return (
    <ul className="space-y-2">
      {payments.map((payment) => (
        <li key={payment.id} className="rounded-md border border-line bg-surface px-3 py-2">
          <div className="flex items-center justify-between gap-2">
            <span className="money text-sm font-semibold tabular-nums text-ink">
              {formatMoney(payment.amountTotal)}
            </span>
            <FiscalStatusBadge payment={payment} />
          </div>

          {payment.status === 'failed' && (
            <>
              <p className="mt-1 text-xs leading-snug text-ink-dim">
                {payment.failureReason ?? 'Mali kayıt tamamlanamadı.'}
              </p>
              <button
                type="button"
                onClick={() => onRetry(payment)}
                className="mt-2 min-h-11 w-full rounded-md bg-amber px-3 font-semibold text-amber-ink"
              >
                Yeniden dene
              </button>
            </>
          )}
        </li>
      ))}
    </ul>
  )
}
