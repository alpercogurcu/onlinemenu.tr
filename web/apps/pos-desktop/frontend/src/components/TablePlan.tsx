import type { main } from '../../wailsjs/go/models'

type TablePlanProps = {
  zones: main.ZonePlanDTO[]
  loading: boolean
  errorMessage: string
  onSelectAvailable: (table: main.TableDTO) => void
  onSelectOccupied: (checkId: string) => void
}

/**
 * Full-panel floor plan shown in the center column while no adisyon is
 * selected — "yeni adisyon" akışı starts here instead of a free-text table
 * label. Card status is never color-only (WCAG): occupied/reserved/cleaning
 * each also carry a text label, and cleaning additionally gets a cross-hatch
 * pattern (see style.css's .table-cleaning-pattern).
 *
 * Tap behavior (see App.tsx's handleSelectTable/handleSelectCheck):
 *  - empty/reserved  -> open a new check against this table (onSelectAvailable)
 *  - occupied        -> jump to the check already open on it (onSelectOccupied)
 *  - cleaning        -> not tappable (disabled, both visually and via the
 *                       button's disabled attribute)
 */
export function TablePlan({ zones, loading, errorMessage, onSelectAvailable, onSelectOccupied }: TablePlanProps) {
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
}: {
  table: main.TableDTO
  onSelectAvailable: (table: main.TableDTO) => void
  onSelectOccupied: (checkId: string) => void
}) {
  const isOccupied = table.status === 'occupied'
  const isReserved = table.status === 'reserved'
  const isCleaning = table.status === 'cleaning'

  let variant = 'border-line bg-panel text-ink' // empty (default)
  if (isOccupied) variant = 'border-amber bg-amber font-semibold text-amber-ink'
  else if (isReserved) variant = 'border-2 border-teal bg-panel text-ink'
  else if (isCleaning) variant = 'table-cleaning-pattern border-line bg-panel text-ink-dim'

  function handleClick() {
    if (isCleaning) return
    if (isOccupied) {
      if (table.active_check_id) onSelectOccupied(table.active_check_id)
      return
    }
    onSelectAvailable(table)
  }

  return (
    <button
      type="button"
      disabled={isCleaning}
      onClick={handleClick}
      className={`flex min-h-14 flex-col items-center justify-center gap-0.5 rounded-md border px-2 py-2 text-center transition-colors disabled:cursor-not-allowed ${variant}`}
    >
      <span className="block font-medium leading-tight">{table.name}</span>
      <span className="block text-xs opacity-80">{table.capacity} kişi</span>
      {isReserved && <span className="block text-[10px] uppercase tracking-wide">Rezerve</span>}
      {isCleaning && <span className="block text-[10px] uppercase tracking-wide">Temizlik</span>}
    </button>
  )
}
