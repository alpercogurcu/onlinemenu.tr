import { useState } from 'react'
import type { main } from '../../wailsjs/go/models'
import type { PendingLine } from '../lib/cart'
import { pendingLineTotal } from '../lib/cart'
import { formatMoney, parseMoneyInputToKurus } from '../lib/format'
import { ErrorBanner } from './ErrorBanner'
import { HoldButton } from './HoldButton'

const QUICK_NOTES = [5000, 10000, 20000, 50000] // ₺50 / ₺100 / ₺200 / ₺500 in kuruş

type ReceiptProps = {
  tableLabel: string
  confirmedOrders: main.OrderDTO[]
  pendingLines: PendingLine[]
  onRemovePendingLine: (clientId: string) => void
  onSendOrder: () => Promise<void>
  sendingOrder: boolean
  confirmedTotal: number
  pendingTotal: number
  hasPaid: boolean
  /**
   * Sum of completed payments already recorded against this check (from
   * App's ListCheckPayments call on selection) — shown so a cashier
   * reopening an adisyon that was already paid (e.g. after an app restart
   * between payment and close) sees that immediately, instead of risking a
   * second cash payment for the same check.
   */
  alreadyPaidTotal: number
  /**
   * receivedAmount (the cash the customer physically handed over, in
   * kuruş) is passed alongside amountTotal so the parent (App) can hold
   * onto it for the receipt print that follows CloseCheck — this
   * component's own receivedInput is cleared on confirm (see
   * handleConfirmPayment below) well before CloseCheck is even called
   * (cash mode closes immediately; the "Basılı tutup kapat" hold button is
   * a separate, later step), so App is the only place left that still
   * knows this value at print time.
   */
  onRegisterPayment: (amountTotal: number, receivedAmount: number) => Promise<void>
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
  hasPaid,
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
  const canPay = pendingLines.length === 0 && confirmedTotal > 0 && !hasPaid

  const receivedKurus = parseMoneyInputToKurus(receivedInput)
  const changeDue = Math.max(0, receivedKurus - confirmedTotal)
  const receivedEnough = receivedKurus >= confirmedTotal && confirmedTotal > 0

  async function handleConfirmPayment() {
    setSubmittingPayment(true)
    try {
      await onRegisterPayment(confirmedTotal, receivedKurus)
      setCashMode(false)
      setReceivedInput('')
    } catch {
      // errorMessage is already derived from onRegisterPayment's own state
      // update in the parent (App) — nothing further to do here besides
      // staying in cash mode so the cashier can retry.
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

            {!hasPaid && (
              <button
                type="button"
                disabled={!canPay}
                onClick={() => setCashMode(true)}
                className="min-h-14 w-full rounded-lg bg-amber px-4 font-display text-lg font-bold text-amber-ink disabled:opacity-40"
              >
                Nakit al
              </button>
            )}
            {!hasPaid && pendingLines.length > 0 && (
              <p className="text-center text-xs text-ink-dim">Önce siparişi gönderin</p>
            )}

            {hasPaid && (
              <HoldButton label="Basılı tutup kapat" holdingLabel="Kapatılıyor…" onConfirm={onCloseCheck} />
            )}
          </div>
        </>
      )}

      {cashMode && (
        <div className="flex flex-1 flex-col justify-between p-4">
          <div>
            <p className="text-ink-dim">Tutar</p>
            <p className="money font-display text-4xl font-bold tabular-nums text-ink">
              {formatMoney(confirmedTotal)}
            </p>

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
                onClick={() => setReceivedInput((confirmedTotal / 100).toFixed(2).replace('.', ','))}
                className="min-h-14 rounded-md border border-line bg-surface font-semibold text-ink"
              >
                Tam
              </button>
            </div>

            <label className="mt-4 block text-sm text-ink-dim" htmlFor="received-amount">
              Alınan tutar
            </label>
            <input
              id="received-amount"
              inputMode="decimal"
              className="money min-h-14 w-full rounded-md border border-line bg-surface px-3 py-2 text-xl tabular-nums text-ink"
              value={receivedInput}
              onChange={(e) => setReceivedInput(e.target.value)}
              autoFocus
            />

            <div className="mt-4">
              <p className="text-ink-dim">Para üstü</p>
              <p
                key={changeDue}
                className={`money font-display text-5xl font-bold tabular-nums text-teal ${
                  receivedEnough ? 'change-due-pulse' : ''
                }`}
              >
                {formatMoney(changeDue)}
              </p>
            </div>
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
