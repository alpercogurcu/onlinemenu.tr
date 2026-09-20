import type { main } from '../../wailsjs/go/models'
import { canPickTable, type TargetKind } from '../lib/checkActions'
import { PendingFiscalDot } from './PendingFiscalDot'

type TablePlanProps = {
  zones: main.ZonePlanDTO[]
  loading: boolean
  errorMessage: string
  onSelectAvailable: (table: main.TableDTO) => void
  onSelectOccupied: (checkId: string) => void
  /** A wiped table is freed with one tap (it turns "cleaning" when its adisyon closes). */
  onCleanTable: (table: main.TableDTO) => void
  /** Checks with a payment awaiting its fiscal record — the table holding one
   * gets a warn indicator (requirement 5). */
  awaitingFiscalCheckIds: ReadonlySet<string>
  /** Target-selection mode (transfer / merge / move items): the plan is used to
   * pick where an adisyon goes instead of opening one. */
  target?: TargetSelection
}

export type TargetSelection = {
  kind: TargetKind
  /** Header text, e.g. "Hedef masayı seçin — Masa 4 taşınacak". */
  prompt: string
  /** The adisyon being moved — its own table is never a valid target. */
  currentCheckId: string
  onPick: (table: main.TableDTO) => void
  onCancel: () => void
}

/**
 * Full-panel floor plan shown in the center column while no adisyon is
 * selected — "yeni adisyon" akışı starts here instead of a free-text table
 * label. Card status is never color-only (WCAG): occupied/reserved/cleaning
 * each also carry a text label ("Dolu"/"Rezerve"/"Temizlik"), and cleaning
 * additionally gets a cross-hatch pattern (see style.css's
 * .table-cleaning-pattern). Occupied is slate, not amber: amber is reserved
 * for money/primary actions (docs/pos-ux-spec.md §2 ilke 7).
 *
 * Tap behavior (see App.tsx's handleSelectTable/handleSelectCheck):
 *  - empty/reserved  -> open a new check against this table (onSelectAvailable)
 *  - occupied        -> jump to the check already open on it (onSelectOccupied)
 *  - cleaning        -> one tap frees it ("Temizlendi → boşalt", onCleanTable):
 *                       closing an adisyon leaves its table "cleaning", and the
 *                       counter must be able to reopen it once it is wiped
 */
export function TablePlan(props: TablePlanProps) {
  const { target } = props
  if (!target) return <TablePlanBody {...props} />
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <div className="flex shrink-0 items-center justify-between gap-3 border-b border-line bg-panel px-4 py-2">
        <h2 className="font-display text-lg font-bold text-ink">{target.prompt}</h2>
        <button
          type="button"
          onClick={target.onCancel}
          className="min-h-12 shrink-0 rounded-md border border-line px-4 font-semibold text-ink"
        >
          İptal
        </button>
      </div>
      <TablePlanBody {...props} />
    </div>
  )
}

function TablePlanBody({
  zones,
  loading,
  errorMessage,
  onSelectAvailable,
  onSelectOccupied,
  onCleanTable,
  awaitingFiscalCheckIds,
  target,
}: TablePlanProps) {
  // Fail-open once the plan has data: a transient failure on the 30s
  // background refresh (see App.tsx's refreshTables) must not blank out an
  // already-drawn, still-usable plan — that would make every table
  // untappable for up to 30s on a momentary connectivity blip, which is
  // worse than showing a slightly stale plan. Only a genuine "never loaded"
  // failure (no zones yet) blocks the whole screen with the error.
  if (errorMessage && zones.length === 0) {
    return (
      <div className="flex flex-1 items-center justify-center p-4 text-center text-danger">{errorMessage}</div>
    )
  }

  if (loading && zones.length === 0) {
    return <div className="flex flex-1 items-center justify-center text-ink-dim">Masa planı yükleniyor…</div>
  }

  if (zones.length === 0) {
    return (
      <div className="flex flex-1 items-center justify-center p-4 text-center text-ink-dim">
        Bu şube için tanımlı masa yok — &quot;Paket servis&quot; ile masasız satış açabilirsiniz.
      </div>
    )
  }

  return (
    <div className="flex-1 overflow-y-auto p-4">
      {zones.map((zone) => (
        <section key={zone.zone_id} className="mb-6">
          <h3 className="mb-2 font-display text-sm font-bold uppercase tracking-wide text-ink-dim">
            {zone.zone_name} <span className="normal-case text-ink-dim">· Kat {zone.floor}</span>
          </h3>
          <div className="grid grid-cols-[repeat(auto-fill,minmax(120px,1fr))] gap-3">
            {zone.tables.map((table) => (
              <TableCard
                key={table.id}
                table={table}
                onSelectAvailable={onSelectAvailable}
                onSelectOccupied={onSelectOccupied}
                onCleanTable={onCleanTable}
                target={target}
                awaitingFiscal={Boolean(table.active_check_id && awaitingFiscalCheckIds.has(table.active_check_id))}
              />
            ))}
          </div>
        </section>
      ))}
    </div>
  )
}

function TableCard({
  table,
  onSelectAvailable,
  onSelectOccupied,
  onCleanTable,
  awaitingFiscal,
  target,
}: {
  table: main.TableDTO
  onSelectAvailable: (table: main.TableDTO) => void
  onSelectOccupied: (checkId: string) => void
  onCleanTable: (table: main.TableDTO) => void
  awaitingFiscal: boolean
  target?: TargetSelection
}) {
  const isOccupied = table.status === 'occupied'
  const isReserved = table.status === 'reserved'
  const isCleaning = table.status === 'cleaning'

  let variant = 'border-line bg-panel text-ink' // empty (default)
  if (isOccupied) variant = 'border-2 border-occupied-line bg-occupied font-semibold text-ink'
  else if (isReserved) variant = 'border-2 border-teal bg-panel text-ink'
  else if (isCleaning) variant = 'table-cleaning-pattern border-line bg-panel text-ink-dim'

  // In target mode a card is either a valid destination (tappable, outlined) or
  // dimmed and inert; the normal open/jump behavior is off.
  const pickable = target ? canPickTable(target.kind, table, target.currentCheckId) : false

  function handleClick() {
    if (target) {
      if (pickable) target.onPick(table)
      return
    }
    if (isCleaning) {
      onCleanTable(table)
      return
    }
    if (isOccupied) {
      if (table.active_check_id) onSelectOccupied(table.active_check_id)
      return
    }
    onSelectAvailable(table)
  }

  return (
    <button
      type="button"
      disabled={target ? !pickable : false}
      onClick={handleClick}
      className={`relative flex min-h-14 flex-col items-center justify-center gap-0.5 rounded-md border px-2 py-2 text-center transition-colors disabled:cursor-not-allowed ${variant} ${
        target ? (pickable ? 'ring-2 ring-amber' : 'opacity-40') : ''
      }`}
    >
      {awaitingFiscal && (
        <span className="absolute right-1.5 top-1.5">
          <PendingFiscalDot />
        </span>
      )}
      <span className="block font-medium leading-tight">{table.name}</span>
      <span className={`block text-xs ${isOccupied ? '' : 'opacity-80'}`}>{table.capacity} kişi</span>
      {isOccupied && <span className="block text-[10px] uppercase tracking-wide">Dolu</span>}
      {isReserved && <span className="block text-[10px] uppercase tracking-wide">Rezerve</span>}
      {isCleaning && (
        <>
          <span className="block text-[10px] uppercase tracking-wide">Temizlik</span>
          <span className="block text-[10px] font-semibold text-ink">Temizlendi → boşalt</span>
        </>
      )}
    </button>
  )
}
