import { useState } from 'react'
import type { main } from '../../wailsjs/go/models'
import { canPickTable, formatMoney, type TargetKind } from '@onlinemenu/pos-core'
import { checkTitle, elapsedLabel, SERVICE_LABELS, type ServiceKind } from '../lib/checkDisplay'
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
  /** Open checks by id, so an occupied card can show its adisyon's running
   * total and elapsed time (active_check_id -> check). */
  openChecksById?: ReadonlyMap<string, main.CheckDTO>
  /** Masasız servis adisyonları for the Gel Al / Paket tabs (AnaEkran
   * tasarımı). Absent in target-selection mode: a takeaway check is never a
   * transfer/merge destination, only tables are. */
  serviceTabs?: ServiceTabs
  /** Target-selection mode (transfer / merge / move items): the plan is used to
   * pick where an adisyon goes instead of opening one. */
  target?: TargetSelection
}

export type ServiceTabs = {
  takeaway: main.CheckDTO[]
  delivery: main.CheckDTO[]
  onSelectCheck: (checkId: string) => void
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
 * AnaEkran tasarımı: zone chips above the grid filter it to one zone at a
 * time, and two extra tabs ("Gel Al · N", "Paket") list the masasız servis
 * adisyonları by customer name instead of tables.
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
  openChecksById,
  serviceTabs,
  target,
}: TablePlanProps) {
  // Which chip is active: a zone id or a service tab. null means "first
  // zone" so the default needs no effect when zones arrive async; a selection
  // that no longer exists (zone removed, feed refreshed) falls back the same
  // way instead of stranding the cashier on an empty grid.
  const [selectedTab, setSelectedTab] = useState<string | null>(null)
  const serviceTabsShown = serviceTabs && !target
  const validTabs = new Set<string>(zones.map((z) => z.zone_id))
  if (serviceTabsShown) {
    validTabs.add('takeaway')
    validTabs.add('delivery')
  }
  const activeTab = selectedTab && validTabs.has(selectedTab) ? selectedTab : (zones[0]?.zone_id ?? null)

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

  if (zones.length === 0 && !serviceTabsShown) {
    return (
      <div className="flex flex-1 items-center justify-center p-4 text-center text-ink-dim">
        Bu şube için tanımlı masa yok — &quot;Paket servis&quot; ile masasız satış açabilirsiniz.
      </div>
    )
  }

  const activeZone = zones.find((z) => z.zone_id === activeTab) ?? null

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex shrink-0 flex-wrap gap-2 px-4 pt-4">
        {zones.map((zone) => (
          <TabChip
            key={zone.zone_id}
            active={activeTab === zone.zone_id}
            label={zone.zone_name}
            onSelect={() => setSelectedTab(zone.zone_id)}
          />
        ))}
        {serviceTabsShown && (
          <>
            <TabChip
              active={activeTab === 'takeaway'}
              label={serviceTabs.takeaway.length > 0 ? `Gel Al · ${serviceTabs.takeaway.length}` : 'Gel Al'}
              onSelect={() => setSelectedTab('takeaway')}
            />
            <TabChip
              active={activeTab === 'delivery'}
              label={serviceTabs.delivery.length > 0 ? `Paket · ${serviceTabs.delivery.length}` : 'Paket'}
              onSelect={() => setSelectedTab('delivery')}
            />
          </>
        )}
      </div>

      {serviceTabsShown && (activeTab === 'takeaway' || activeTab === 'delivery') ? (
        <ServiceCheckList
          kind={activeTab}
          checks={activeTab === 'takeaway' ? serviceTabs.takeaway : serviceTabs.delivery}
          onSelectCheck={serviceTabs.onSelectCheck}
        />
      ) : (
        <div className="flex-1 overflow-y-auto p-4">
          {activeZone ? (
            <section>
              <h3 className="mb-2 font-display text-sm font-bold uppercase tracking-wide text-ink-dim">
                {activeZone.zone_name} <span className="normal-case text-ink-dim">· Kat {activeZone.floor}</span>
              </h3>
              <div className="grid grid-cols-[repeat(auto-fill,minmax(120px,1fr))] gap-3">
                {activeZone.tables.map((table) => (
                  <TableCard
                    key={table.id}
                    table={table}
                    check={table.active_check_id ? (openChecksById?.get(table.active_check_id) ?? null) : null}
                    onSelectAvailable={onSelectAvailable}
                    onSelectOccupied={onSelectOccupied}
                    onCleanTable={onCleanTable}
                    target={target}
                    awaitingFiscal={Boolean(
                      table.active_check_id && awaitingFiscalCheckIds.has(table.active_check_id),
                    )}
                  />
                ))}
              </div>
            </section>
          ) : (
            <p className="p-4 text-center text-ink-dim">
              Bu şube için tanımlı masa yok — &quot;Paket servis&quot; ile masasız satış açabilirsiniz.
            </p>
          )}
        </div>
      )}
    </div>
  )
}

function TabChip({ active, label, onSelect }: { active: boolean; label: string; onSelect: () => void }) {
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-pressed={active}
      className={`inline-flex min-h-11 items-center rounded-full px-5 text-sm transition-colors ${
        active ? 'bg-ink font-bold text-surface' : 'border border-line font-semibold text-ink-dim'
      }`}
    >
      {label}
    </button>
  )
}

/**
 * Gel Al / Paket tab content: masasız servis adisyonları as customer-named
 * rows ("GEL AL · Alper Vural — ₺1.030"). Tapping one opens the check,
 * exactly like tapping an occupied table.
 */
function ServiceCheckList({
  kind,
  checks,
  onSelectCheck,
}: {
  kind: ServiceKind
  checks: main.CheckDTO[]
  onSelectCheck: (checkId: string) => void
}) {
  if (checks.length === 0) {
    return (
      <div className="flex flex-1 items-center justify-center p-4 text-center text-ink-dim">
        Bekleyen {SERVICE_LABELS[kind] === 'GEL AL' ? 'gel al' : 'paket'} adisyonu yok.
      </div>
    )
  }
  return (
    <ul className="flex-1 overflow-y-auto p-4">
      {checks.map((check) => (
        <li key={check.id} className="mb-3">
          <button
            type="button"
            onClick={() => onSelectCheck(check.id)}
            className="flex min-h-14 w-full items-center gap-3 rounded-xl border border-line bg-panel px-4 py-3 text-left"
          >
            <span className="flex min-w-0 flex-1 flex-col gap-0.5">
              <span className="truncate font-semibold text-ink">{checkTitle(check)}</span>
              <span className="text-xs text-ink-dim tabular-nums">{elapsedLabel(check.opened_at)}</span>
            </span>
            {check.total !== undefined && check.total !== null && (
              <span className="money shrink-0 font-bold tabular-nums text-ink">{formatMoney(check.total)}</span>
            )}
          </button>
        </li>
      ))}
    </ul>
  )
}

function TableCard({
  table,
  check,
  onSelectAvailable,
  onSelectOccupied,
  onCleanTable,
  awaitingFiscal,
  target,
}: {
  table: main.TableDTO
  /** The open check on this table, when the open-check list knows it — feeds
   * the occupied card's total and elapsed time (AnaEkran tasarımı). */
  check: main.CheckDTO | null
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

  const occupiedElapsed = isOccupied && check ? elapsedLabel(check.opened_at) : ''

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
      {isOccupied && check && check.total !== undefined && check.total !== null && (
        <span className="money block text-sm font-bold tabular-nums">{formatMoney(check.total)}</span>
      )}
      <span className={`block text-xs ${isOccupied ? '' : 'opacity-80'}`}>
        {table.capacity} kişi{occupiedElapsed ? ` · ${occupiedElapsed}` : ''}
      </span>
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
