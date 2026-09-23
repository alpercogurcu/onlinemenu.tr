import { useEffect, useState } from 'react'
import type { main } from '../../wailsjs/go/models'
import { formatMoney, type PendingLine } from '@onlinemenu/pos-core'
import type { RemoteCompletedRow, RemotePendingFiscal, TrackedPayment } from '../lib/fiscalStatus'
import { shortOrderId } from '../lib/kitchenPrint'
import { ErrorBanner } from './ErrorBanner'
import { FiscalStatusBadge } from './FiscalStatusBadge'
import { HoldButton } from './HoldButton'
import { CheckActionsMenu, type CheckActionsDisabled } from './CheckActionsMenu'
import { CheckIcon, ClockIcon } from './icons'
import { PendingLineRow } from './PendingLineRow'

type ReceiptProps = {
  tableLabel: string
  confirmedOrders: main.OrderDTO[]
  pendingLines: PendingLine[]
  onRemovePendingLine: (clientId: string) => void
  onChangePendingQuantity: (clientId: string, delta: number) => void
  onSendOrder: () => Promise<void>
  /** Reprints one order's kitchen ticket; resolves true when it was printed.
   * A failure is reported by the parent's banner, not here. */
  onReprintKitchenTicket: (orderId: string) => Promise<boolean>
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
  /** Branch-wide visibility (SEC-005 follow-up) — payments completed at
   * ANOTHER station (or an earlier session) that already count toward
   * `settledPaidTotal`/`remaining` but have no row of their own in `payments`.
   * Already deduped against `payments` by the caller (see
   * lib/fiscalStatus.buildRemotePaymentRows) — never re-filter here. */
  remoteCompletedPayments: readonly RemoteCompletedRow[]
  /** Same visibility gap for payments still mid-registration at ANOTHER
   * station — the reason `remaining` is lower / the close is blocked even
   * though this station's own `payments` list looks clear. Already deduped. */
  remotePendingPayments: readonly RemotePendingFiscal[]
  /** Opens the payment screen over the middle panel (see PaymentScreen). */
  onStartPayment: () => void
  /** True while the payment screen is open — the rail then drops its own "Ödeme al". */
  paymentActive: boolean
  /** The adisyon's ⋯ menu; null while no adisyon is open. */
  actions: {
    disabled: CheckActionsDisabled
    onTransfer: () => void
    onMerge: () => void
    onMoveItems: () => void
  } | null
  /** The item-selection phase of "Kalem taşı": sent items become tickable. */
  moveSelection: {
    selectedIds: ReadonlySet<string>
    onToggle: (itemId: string) => void
    onPickTarget: () => void
    onCancel: () => void
  } | null
  /** Items already covered by an item payment — they cannot be moved. */
  paidItemIds: ReadonlySet<string>
  /** Result of the last transfer/merge/move, shown briefly (empty when none). */
  notice: string
  /** Requirement 3 — retry a failed payment: the parent drops it from the tracked
   * list (returning its amount to the collectable balance) and reopens the
   * payment screen on that amount and method. */
  onRetryPayment: (payment: TrackedPayment) => void
  onCloseCheck: () => Promise<void>
  errorMessage: string
}

/**
 * Right rail — the signature element: a live thermal-receipt view of the
 * current adisyon. Confirmed (already sent to kitchen) lines are read-only
 * mono rows; unsent lines are the same style but removable (void, red —
 * the only place red appears outside close/cancel). Taking the payment
 * happens on the payment screen (PaymentScreen), which takes over the middle
 * panel; the rail stays as the breakdown next to it.
 *
 * Since ADR-FISCAL-002 a registered payment is not the end of the story: each
 * one carries a fiscal-record status badge until the ÖKC confirms the receipt.
 */
export function Receipt({
  tableLabel,
  confirmedOrders,
  pendingLines,
  onRemovePendingLine,
  onChangePendingQuantity,
  onSendOrder,
  onReprintKitchenTicket,
  sendingOrder,
  confirmedTotal,
  pendingTotal,
  settledPaidTotal,
  remaining,
  isFullyPaid,
  closeBlockReason,
  payments,
  remoteCompletedPayments,
  remotePendingPayments,
  onStartPayment,
  paymentActive,
  actions,
  moveSelection,
  paidItemIds,
  notice,
  onRetryPayment,
  onCloseCheck,
  errorMessage,
}: ReceiptProps) {
  const grandTotal = confirmedTotal + pendingTotal
  const canSendOrder = pendingLines.length > 0 && !sendingOrder

  // `remaining` (not a sticky "hasPaid" flag) still drives everything, but it
  // is now computed by App from three inputs — settled money, pending-in-flight
  // money, and the check total — rather than from a single accumulated total.
  const canPay = pendingLines.length === 0 && confirmedTotal > 0 && remaining > 0

  return (
    <aside className="flex h-full w-96 shrink-0 flex-col border-l border-line bg-panel">
      <div className="flex items-center justify-between gap-2 border-b border-line p-4">
        <h2 className="min-w-0 truncate font-display text-lg font-bold text-ink">{tableLabel || 'Adisyon'}</h2>
        {actions && !moveSelection && (
          <CheckActionsMenu
            disabled={actions.disabled}
            onTransfer={actions.onTransfer}
            onMerge={actions.onMerge}
            onMoveItems={actions.onMoveItems}
          />
        )}
      </div>

      <div className="flex-1 overflow-y-auto px-4 py-2 font-mono text-sm text-ink">
        {notice && (
          <p role="status" className="mb-2 rounded-md bg-teal/10 px-2 py-1 font-sans text-sm text-teal">
            {notice}
          </p>
        )}
        {confirmedOrders.length === 0 && pendingLines.length === 0 && (
          <p className="py-6 text-center text-ink-dim">Adisyon boş — ürün ekleyin.</p>
        )}

        {confirmedOrders.map((order) => (
          <div key={order.id}>
            {order.items.map((item) => {
              const line = (
                <>
                  <div className="flex justify-between gap-2">
                    <span className="qty text-ink-dim">{item.quantity}×</span>
                    <span className="flex-1 truncate">{item.product_name}</span>
                    <span className="money tabular-nums">
                      {formatMoney(item.quantity * item.unit_price_amount)}
                    </span>
                  </div>
                  {item.note && <p className="break-words pl-7 text-xs text-ink-dim">{item.note}</p>}
                </>
              )
              if (!moveSelection) {
                return (
                  <div key={item.id} className="receipt-line-enter py-1">
                    {line}
                  </div>
                )
              }
              const paid = paidItemIds.has(item.id)
              const selected = moveSelection.selectedIds.has(item.id)
              return (
                <button
                  key={item.id}
                  type="button"
                  disabled={paid}
                  aria-pressed={selected}
                  onClick={() => moveSelection.onToggle(item.id)}
                  className={`mb-1 flex min-h-14 w-full items-center gap-3 rounded-md border px-2 py-1 text-left disabled:opacity-50 ${
                    selected ? 'border-amber bg-amber/10' : 'border-line'
                  }`}
                >
                  <span
                    aria-hidden="true"
                    className={`flex h-6 w-6 shrink-0 items-center justify-center rounded border ${
                      selected ? 'border-amber bg-amber text-amber-ink' : 'border-ink-dim'
                    }`}
                  >
                    {selected && <CheckIcon size={16} />}
                  </span>
                  <span className="min-w-0 flex-1">
                    {line}
                    {paid && <span className="block text-xs text-teal">Ödendi — taşınamaz</span>}
                  </span>
                </button>
              )
            })}
            {!moveSelection && <KitchenTicketButton orderId={order.id} onReprint={onReprintKitchenTicket} />}
          </div>
        ))}

        {pendingLines.map((line) => (
          <PendingLineRow
            key={line.clientId}
            line={line}
            onChangeQuantity={onChangePendingQuantity}
            onRemove={onRemovePendingLine}
          />
        ))}
      </div>

      <div className="receipt-tear" aria-hidden="true" />

      {moveSelection ? (
        <div className="space-y-2 p-4">
          <ErrorBanner message={errorMessage} />
          <p className="text-sm text-ink" role="status">
            {moveSelection.selectedIds.size === 0
              ? 'Taşınacak kalemleri seçin.'
              : `${moveSelection.selectedIds.size} kalem seçildi.`}
          </p>
          <button
            type="button"
            disabled={moveSelection.selectedIds.size === 0}
            onClick={moveSelection.onPickTarget}
            className="min-h-14 w-full rounded-lg bg-amber px-4 font-display text-lg font-bold text-amber-ink disabled:opacity-40"
          >
            Hedef masayı seç
          </button>
          <button
            type="button"
            onClick={moveSelection.onCancel}
            className="min-h-14 w-full rounded-lg border border-line px-4 font-medium text-ink-dim"
          >
            Vazgeç
          </button>
        </div>
      ) : (
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

        <PaymentStatusList
          payments={payments}
          remoteCompletedPayments={remoteCompletedPayments}
          remotePendingPayments={remotePendingPayments}
          onRetry={onRetryPayment}
        />

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

        {!isFullyPaid && !paymentActive && (
          <button
            type="button"
            disabled={!canPay}
            onClick={onStartPayment}
            className="min-h-14 w-full rounded-lg bg-amber px-4 font-display text-lg font-bold text-amber-ink disabled:opacity-40"
          >
            Ödeme al
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
      )}
    </aside>
  )
}

/**
 * One row per payment this session registered against the check, each carrying
 * its fiscal-record status (requirement 1). A payment whose registration failed
 * additionally shows the reason and a full-width retry target (requirement 3 —
 * min-h-12 = 48px).
 *
 * Followed by two more row kinds — same rail, same visual language — for
 * money this station did NOT itself register but that already moves
 * `remaining`/`closeBlockReason` (branch-wide fiscal visibility): a payment
 * completed at another till, and one still mid-registration at another till.
 * Both lists arrive already deduped against `payments` (see
 * lib/fiscalStatus.buildRemotePaymentRows) — a payment id never appears twice
 * on this rail.
 *
 * Nothing renders for a check with no payments (own or remote) at all, so the
 * ordinary add-items-and-send flow is visually untouched.
 */
function PaymentStatusList({
  payments,
  remoteCompletedPayments,
  remotePendingPayments,
  onRetry,
}: {
  payments: readonly TrackedPayment[]
  remoteCompletedPayments: readonly RemoteCompletedRow[]
  remotePendingPayments: readonly RemotePendingFiscal[]
  onRetry: (payment: TrackedPayment) => void
}) {
  if (payments.length === 0 && remoteCompletedPayments.length === 0 && remotePendingPayments.length === 0) {
    return null
  }

  return (
    <ul className="space-y-2">
      {payments.map((payment) => (
        <li key={payment.id} className="rounded-md border border-line bg-surface px-3 py-2">
          <div className="flex items-center justify-between gap-2">
            <span className="money text-sm font-semibold tabular-nums text-ink">
              {formatMoney(payment.amountTotal)}{' '}
              {payment.method && (
                <span className="text-xs font-normal text-ink-dim">{payment.method === 'card' ? 'Kart' : 'Nakit'}</span>
              )}
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
                className="mt-2 min-h-12 w-full rounded-md bg-amber px-3 font-semibold text-amber-ink"
              >
                Yeniden dene
              </button>
            </>
          )}
        </li>
      ))}

      {/* Settled money this station did not itself register — either taken at
          ANOTHER station, or by this same station in an EARLIER session (app
          restart, shift change) whose tracked list is gone. Same "settled"
          visual language as a tracked completed row (teal); the label makes
          no station claim on purpose since either case is possible — see
          buildRemotePaymentRows' NAMING CAVEAT. */}
      {remoteCompletedPayments.map((row) => (
        <li key={row.paymentId} className="rounded-md border border-line bg-surface px-3 py-2">
          <div className="flex items-center justify-between gap-2">
            <span className="money text-sm font-semibold tabular-nums text-ink">
              {formatMoney(row.amountTotal)}
            </span>
            <span className="inline-flex items-center gap-1.5 rounded-full bg-teal/15 px-2 py-0.5 text-xs font-semibold text-teal">
              <CheckIcon size={14} />
              Daha önce tahsil edildi
            </span>
          </div>
        </li>
      ))}

      {/* Still mid-registration somewhere this station cannot see (another
          station, or this station's own earlier session — same caveat as
          above) — dimmed, this station has no retry affordance for it, same
          grey token FiscalStatusBadge already uses for a status this session
          cannot act on (voided/unknown). */}
      {remotePendingPayments.map((row) => (
        <li key={row.paymentId} className="rounded-md border border-line/60 bg-surface/60 px-3 py-2 opacity-70">
          <div className="flex items-center justify-between gap-2">
            <span className="money text-sm font-semibold tabular-nums text-ink-dim">
              {formatMoney(row.amountTotal)}
            </span>
            <span className="inline-flex items-center gap-1.5 rounded-full bg-line/40 px-2 py-0.5 text-xs font-semibold text-ink-dim">
              <ClockIcon size={14} />
              Başka işlemde • mali kayıt bekleniyor
            </span>
          </div>
        </li>
      ))}
    </ul>
  )
}

const KITCHEN_DONE_FLASH_MS = 2500

/**
 * Small per-order "Mutfak fişi" reprint control under a sent order's lines.
 * Deliberately quiet (text-xs, no fill): the cashier reaches for it rarely —
 * after a failed auto-print the App-level banner is the loud path. It only
 * confirms success itself ("Yazdırıldı"); failures are reported by that banner.
 */
function KitchenTicketButton({
  orderId,
  onReprint,
}: {
  orderId: string
  onReprint: (orderId: string) => Promise<boolean>
}) {
  const [state, setState] = useState<'idle' | 'busy' | 'done'>('idle')

  useEffect(() => {
    if (state !== 'done') return
    const timer = setTimeout(() => setState('idle'), KITCHEN_DONE_FLASH_MS)
    return () => clearTimeout(timer)
  }, [state])

  async function handleClick() {
    setState('busy')
    const printed = await onReprint(orderId)
    setState(printed ? 'done' : 'idle')
  }

  return (
    <div className="flex justify-end pb-1">
      <button
        type="button"
        disabled={state === 'busy'}
        onClick={handleClick}
        title={`Sipariş #${shortOrderId(orderId)} için mutfak fişini yeniden yazdır`}
        className="min-h-12 rounded px-3 font-sans text-xs text-ink-dim underline-offset-2 hover:underline disabled:opacity-50"
      >
        {state === 'busy' ? 'Yazdırılıyor…' : state === 'done' ? 'Yazdırıldı' : 'Mutfak fişi'}
      </button>
    </div>
  )
}
