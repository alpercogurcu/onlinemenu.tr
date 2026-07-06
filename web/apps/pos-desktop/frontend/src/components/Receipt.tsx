import { useState } from 'react'
import type { main } from '../../wailsjs/go/models'
import type { PendingLine } from '../lib/cart'
import { pendingLineTotal } from '../lib/cart'
import { formatMoney, parseMoneyInputToKurus } from '../lib/format'
import { changeDue as computeChangeDue, clampToRemaining, remainingBalance, splitSuggestion } from '../lib/payment'
import { ErrorBanner } from './ErrorBanner'
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

type ReceiptProps = {
  tableLabel: string
  confirmedOrders: main.OrderDTO[]
  pendingLines: PendingLine[]
  onRemovePendingLine: (clientId: string) => void
  onSendOrder: () => Promise<void>
  sendingOrder: boolean
  confirmedTotal: number
  pendingTotal: number
  /**
   * Sum of completed cash payments already recorded against this check
   * (accumulated in App as each RegisterCashPayment call succeeds, seeded
   * from ListCheckPayments on selection where the session's role allows
   * that read). `remainingBalance(confirmedTotal, alreadyPaidTotal)` — not
   * a sticky "hasPaid" boolean — is what actually drives every "is this
   * check paid" decision below, so a split/partial payment is reflected
   * immediately: the check stays payable (and open) for as many
   * installments as it takes to bring this down to zero.
   */
  alreadyPaidTotal: number
  /**
   * amountToRegister is the CLAMPED amount for this one cash-payment step
   * (never more than the remaining balance — see lib/payment's
   * clampToRemaining) — this is what actually gets sent to
   * RegisterCashPayment. receivedAmount is the raw cash the customer
   * physically handed over for this step (which may exceed
   * amountToRegister when this step's change is due) — passed alongside so
   * the parent (App) can accumulate it for the receipt print that follows
   * CloseCheck. This component's own receivedInput is cleared after each
   * confirmed step (see handleConfirmPayment below) — App is the only
   * place left that still knows the cumulative received total by print
   * time.
   */
  onRegisterPayment: (amountToRegister: number, receivedAmount: number) => Promise<void>
  onCloseCheck: () => Promise<void>
  errorMessage: string
}

/**
 * Right rail — the signature element: a live thermal-receipt view of the
 * current adisyon. Confirmed (already sent to kitchen) lines are read-only
 * mono rows; unsent lines are the same style but removable (void, red —
 * the only place red appears outside close/cancel). Cash payment mode
 * expands this panel in place instead of opening a modal.
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
  alreadyPaidTotal,
  onRegisterPayment,
  onCloseCheck,
  errorMessage,
}: ReceiptProps) {
  const [cashMode, setCashMode] = useState(false)
  const [receivedInput, setReceivedInput] = useState('')
  const [submittingPayment, setSubmittingPayment] = useState(false)

  const grandTotal = confirmedTotal + pendingTotal
  const canSendOrder = pendingLines.length > 0 && !sendingOrder

  // remaining is the single source of truth for "is this check paid" — a
  // check with confirmedTotal=630 and alreadyPaidTotal=50 is neither
  // "unpaid" nor "paid": it is 580 short, still open, still payable in
  // further installments. There is no sticky "hasPaid" flag anymore (that
  // was the bug: it went true after the FIRST partial payment and hid the
  // "Nakit al" button, making a second installment impossible).
  const remaining = remainingBalance(confirmedTotal, alreadyPaidTotal)
  const isFullyPaid = confirmedTotal > 0 && remaining <= 0
  const canPay = pendingLines.length === 0 && confirmedTotal > 0 && !isFullyPaid

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
  // Enabled for ANY positive entry, not just one covering the full
  // remaining balance — this is the actual fix for the reported bug (a
  // ₺50 entry against a ₺630 remaining balance must be payable).
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

            {alreadyPaidTotal > 0 && (
              <div className="flex items-baseline justify-between rounded-md bg-teal/10 px-2 py-1 text-sm">
                <span className="text-ink-dim">Önceden ödenen</span>
                <span className="money font-semibold tabular-nums text-teal">
                  {formatMoney(alreadyPaidTotal)}
                </span>
              </div>
            )}

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

            {isFullyPaid && (
              <HoldButton label="Basılı tutup kapat" holdingLabel="Kapatılıyor…" onConfirm={onCloseCheck} />
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
            {alreadyPaidTotal > 0 && (
              <p className="mt-1 text-xs text-ink-dim">
                {formatMoney(confirmedTotal)} hesaptan {formatMoney(alreadyPaidTotal)} ödendi
              </p>
            )}

            <div className="mt-4 grid grid-cols-3 gap-2">
              {QUICK_NOTES.map((note) => (
                <button
                  key={note}
                  type="button"
                  onClick={() => setReceivedInput((note / 100).toFixed(2).replace('.', ','))}
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
                  onClick={() =>
                    setReceivedInput((splitSuggestion(remaining, parts) / 100).toFixed(2).replace('.', ','))
                  }
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
