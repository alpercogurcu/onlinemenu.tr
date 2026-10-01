import { formatMoney } from '@onlinemenu/pos-core'

// GÜN ÖZETİ (kasa kapanış ekranı) — presentational section + the pure
// derivation feeding it. Both live here, NOT in hooks/useCashSession.ts, so
// this module stays importable without the generated (gitignored) wailsjs
// bindings and the section is render-testable in CI with react-dom/server —
// same rationale as lib/branchFiscal.ts's hand-declared wire types.

/** Narrow wire shape this module needs from a SaleDetailsDTO-like value —
 * hand-declared rather than imported from '../../wailsjs/go/models' (see this
 * file's header comment). */
export type SaleDetailsSource = {
  sales: { closed_check_count: number; gross: number }
  payments: { method: string; status: string; total: number }[]
}

export type DaySummary = {
  closedCheckCount: number
  gross: number
  cashTotal: number
  cardTotal: number
}

/**
 * Collapses the report's (method, status) payment buckets into the two
 * tahsilat figures the kapanış screen shows. Only "completed" payments count
 * as money in hand — pending is unresolved (ÖKC), failed never collected,
 * voided was collected and then cancelled on the device. "terminal" is the
 * card-via-ÖKC method (payment/domain.PaymentMethodTerminal); meal_card/
 * comp/no_charge/open_account are neither nakit nor kart, so they appear in
 * Brüt satış but in neither tahsilat card — deliberate, matching what the
 * cashier can physically reconcile at the drawer.
 */
export function deriveDaySummary(details: SaleDetailsSource): DaySummary {
  let cashTotal = 0
  let cardTotal = 0
  for (const p of details.payments) {
    if (p.status !== 'completed') continue
    if (p.method === 'cash') cashTotal += p.total
    else if (p.method === 'terminal') cardTotal += p.total
  }
  return {
    closedCheckCount: details.sales.closed_check_count,
    gross: details.sales.gross,
    cashTotal,
    cardTotal,
  }
}

type DaySummarySectionProps = {
  summary: DaySummary | null
  loading: boolean
  /** Short Turkish failure text (already via describeError). Non-empty means
   * the last load failed — the section shows it with a retry button instead
   * of silently hiding (task brief: sessizce gizlenmek YOK). */
  error: string
  onRetry: () => void
}

const CARD_CLASS = 'flex flex-col gap-1 rounded-lg border border-line bg-surface px-4 py-3'
const CARD_LABEL_CLASS = 'text-xs text-ink-dim'
const CARD_VALUE_CLASS = 'font-display text-2xl font-bold tabular-nums text-ink'

export function DaySummarySection({ summary, loading, error, onRetry }: DaySummarySectionProps) {
  return (
    <section aria-label="Gün özeti" className="flex flex-col gap-3">
      <h3 className="text-xs font-bold tracking-wider text-ink-dim">GÜN ÖZETİ</h3>

      {error !== '' ? (
        <div className="rounded-md border border-warn bg-warn/10 px-3 py-2 text-sm text-ink" role="alert">
          <p>Gün özeti yüklenemedi: {error}</p>
          <button
            type="button"
            onClick={onRetry}
            disabled={loading}
            className="mt-2 min-h-12 rounded border border-line px-3 text-sm font-semibold text-ink disabled:opacity-40"
          >
            {loading ? 'Yükleniyor…' : 'Yeniden dene'}
          </button>
        </div>
      ) : loading && !summary ? (
        <div className="grid grid-cols-2 gap-3" aria-busy="true" aria-label="Gün özeti yükleniyor">
          {['Kapanan adisyon', 'Brüt satış', 'Nakit tahsilat', 'Kart tahsilat'].map((label) => (
            <div key={label} className={CARD_CLASS}>
              <span className={CARD_LABEL_CLASS}>{label}</span>
              <span className="mt-1 h-7 w-24 animate-pulse rounded bg-line/60" />
            </div>
          ))}
        </div>
      ) : summary ? (
        <div className="grid grid-cols-2 gap-3">
          <div className={CARD_CLASS}>
            <span className={CARD_LABEL_CLASS}>Kapanan adisyon</span>
            <span className={CARD_VALUE_CLASS}>{summary.closedCheckCount}</span>
          </div>
          <div className={CARD_CLASS}>
            <span className={CARD_LABEL_CLASS}>Brüt satış</span>
            <span className={CARD_VALUE_CLASS}>{formatMoney(summary.gross)}</span>
          </div>
          <div className={CARD_CLASS}>
            <span className={CARD_LABEL_CLASS}>Nakit tahsilat</span>
            <span className={CARD_VALUE_CLASS}>{formatMoney(summary.cashTotal)}</span>
          </div>
          <div className={CARD_CLASS}>
            <span className={CARD_LABEL_CLASS}>Kart tahsilat</span>
            <span className={CARD_VALUE_CLASS}>{formatMoney(summary.cardTotal)}</span>
          </div>
        </div>
      ) : null}
    </section>
  )
}
