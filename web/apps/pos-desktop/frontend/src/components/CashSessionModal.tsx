import { useEffect, useState } from 'react'
import type { main } from '../../wailsjs/go/models'
import { computeDifference, emptyDenominationRows, type ClosingSnapshot, type DenominationRow } from '../lib/cashSession'
import { formatMoney, parseMoneyInputToKurus } from '../lib/format'
import { AmountDisplay } from './AmountDisplay'
import { DenominationCounter } from './DenominationCounter'
import { ErrorBanner } from './ErrorBanner'
import { HoldButton } from './HoldButton'
import { Numpad } from './Numpad'

// Ready-made amounts and reasons keep the common cases to one tap: the kiosk
// has no keyboard, so free text is the slow path (docs/pos-ux-spec.md §2 ilke 6).
const OPENING_PRESETS: { label: string; input: string }[] = [
  { label: '₺0', input: '0' },
  { label: '₺500', input: '500' },
  { label: '₺1.000', input: '1000' },
]

const MOVEMENT_REASONS: Record<'in' | 'out', string[]> = {
  in: ['Bozuk para takviyesi', 'Ek nakit girişi', 'Diğer'],
  out: ['Bankaya yatırma', 'Gider ödemesi', 'Bozuk para değişimi', 'Diğer'],
}

type View = 'opening' | 'status' | 'movement' | 'ledger' | 'counting' | 'closing'

type CashSessionModalProps = {
  open: boolean
  onClose: () => void
  checked: boolean
  loading: boolean
  session: main.CashSessionDTO | null
  error: string
  closingSnapshot: ClosingSnapshot | null
  stale: boolean
  cannotCloseReasons: string[] | null
  /** The full hareket defteri — empty until onLoadMovements resolves at least
   * once for the current session (see the 'ledger' view below). */
  movements: main.CashMovementDTO[]
  canOpenSession: boolean
  onOpenSession: (openingCountedAmount: number, openingNotes: string) => Promise<boolean>
  onRecordMovement: (direction: 'in' | 'out', amountMinor: number, reason: string) => Promise<boolean>
  onSubmitClosingCount: (rows: readonly DenominationRow[], notes: string) => Promise<boolean>
  onCloseSession: () => Promise<boolean>
  onDismissCannotClose: () => void
  onRefresh: () => Promise<main.CashSessionDTO | null>
  onLoadMovements: () => Promise<void>
}

/**
 * ADR-DATA-008 kasa oturumu: açılış / durum + nakit giriş-çıkış / sayım /
 * kapanış in one modal, switched by `view`. Kept as one file (matching
 * Receipt.tsx's size precedent) rather than five components — the screens
 * share enough state (session, closingSnapshot) that splitting them would
 * mean threading the same dozen props through an extra layer for no reuse
 * benefit; DenominationCounter is pulled out separately because IT is
 * reused conceptually (sayım input) and independently testable-shaped.
 *
 * `view` defaults from `session`/`open` via the effect below, but is
 * otherwise cashier-navigated (movement/counting are reached from status;
 * counting is also reachable from closing, for a deliberate recount).
 */
export function CashSessionModal({
  open,
  onClose,
  checked,
  loading,
  session,
  error,
  closingSnapshot,
  stale,
  cannotCloseReasons,
  movements,
  canOpenSession,
  onOpenSession,
  onRecordMovement,
  onSubmitClosingCount,
  onCloseSession,
  onDismissCannotClose,
  onRefresh,
  onLoadMovements,
}: CashSessionModalProps) {
  const [view, setView] = useState<View>('opening')
  const [movementsLoading, setMovementsLoading] = useState(false)

  // Deliberately keyed on session?.status (and open), NOT the whole `session`
  // object: a closing_control poll tick (see useCashSession's staleness poll)
  // updates `session` every 5s without changing its status, and re-running
  // this effect on every such tick would forcibly snap the cashier's view
  // back to 'closing' mid-recount, discarding an in-progress 'counting' visit
  // for no reason. Only an actual STATUS transition (or the modal opening,
  // or a session appearing/disappearing) should pick a new default view.
  useEffect(() => {
    if (!open) return
    if (!session) {
      setView('opening')
      return
    }
    if (session.status === 'closing_control') {
      setView('closing')
      return
    }
    setView('status')
  }, [open, session?.status, session?.id])

  const [openingInput, setOpeningInput] = useState('')
  // After a preset chip the next key starts a new amount instead of appending.
  const [openingIsPreset, setOpeningIsPreset] = useState(false)
  const [openingNotes, setOpeningNotes] = useState('')
  const [openingNotesShown, setOpeningNotesShown] = useState(false)
  const [openingSubmitting, setOpeningSubmitting] = useState(false)

  const [direction, setDirection] = useState<'in' | 'out'>('in')
  const [movementInput, setMovementInput] = useState('')
  const [movementReason, setMovementReason] = useState('')
  const [movementCustomReason, setMovementCustomReason] = useState(false)
  const [movementSubmitting, setMovementSubmitting] = useState(false)

  const [rows, setRows] = useState<DenominationRow[]>(emptyDenominationRows())
  const [countingNotes, setCountingNotes] = useState('')
  const [countingNotesShown, setCountingNotesShown] = useState(false)
  const [countingSubmitting, setCountingSubmitting] = useState(false)

  const [closeSubmitting, setCloseSubmitting] = useState(false)
  const [refreshing, setRefreshing] = useState(false)

  if (!open) return null

  const openingAmount = parseMoneyInputToKurus(openingInput)
  const movementAmount = parseMoneyInputToKurus(movementInput)

  async function handleOpenSubmit() {
    setOpeningSubmitting(true)
    const ok = await onOpenSession(openingAmount, openingNotes)
    setOpeningSubmitting(false)
    if (ok) {
      setOpeningInput('')
      setOpeningIsPreset(false)
      setOpeningNotes('')
      setOpeningNotesShown(false)
    }
  }

  function changeDirection(next: 'in' | 'out') {
    if (next === direction) return
    setDirection(next)
    // The reason chips differ per direction; a reason chosen for "giriş" makes
    // no sense on "çıkış".
    setMovementReason('')
    setMovementCustomReason(false)
  }

  async function handleOpenLedger() {
    setView('ledger')
    setMovementsLoading(true)
    await onLoadMovements()
    setMovementsLoading(false)
  }

  async function handleMovementSubmit() {
    setMovementSubmitting(true)
    const ok = await onRecordMovement(direction, movementAmount, movementReason)
    setMovementSubmitting(false)
    if (ok) {
      setMovementInput('')
      setMovementReason('')
      setMovementCustomReason(false)
      setView('status')
    }
  }

  async function handleCountingSubmit() {
    setCountingSubmitting(true)
    const ok = await onSubmitClosingCount(rows, countingNotes)
    setCountingSubmitting(false)
    if (ok) {
      setRows(emptyDenominationRows())
      setCountingNotes('')
      setCountingNotesShown(false)
      // view flips to 'closing' via the effect once `session.status` updates.
    }
  }

  async function handleCloseSubmit() {
    setCloseSubmitting(true)
    await onCloseSession()
    setCloseSubmitting(false)
  }

  async function handleRefresh() {
    setRefreshing(true)
    await onRefresh()
    // A refusal reason is only meaningful relative to the moment it was
    // returned — re-reading (this button's whole purpose) means the cashier
    // is checking whether the blocker is gone. Without clearing it here, a
    // cashier who fixes the underlying condition (e.g. the ÖKC finishes
    // resolving a pending submission) and taps "Durumu yenile" would keep
    // seeing the stale refusal and no close button, with no way back short of
    // "Anladım" first — surprising given the button they just pressed.
    onDismissCannotClose()
    setRefreshing(false)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
      <div
        className={`flex max-h-[90vh] w-full flex-col overflow-hidden rounded-lg border border-line bg-panel ${
          view === 'movement' ? 'max-w-3xl' : 'max-w-md'
        }`}
      >
        <div className="flex shrink-0 items-center justify-between border-b border-line px-4 py-3">
          <h2 className="font-display text-lg font-bold text-ink">
            Kasa
            {/* Non-structural use of `loading`: a subtle inline hint during a
                background refresh (mount, or the 5s closing_control staleness
                poll — see useCashSession.ts), never a reason to unmount the
                current view. Swapping the whole modal body for "Yükleniyor…"
                on every poll tick would flicker the one screen (Kapanış) that
                most needs to stay readable, and could tear down a HoldButton
                mid-press (see closeSession's own pre-close refresh). */}
            {loading && checked && <span className="ml-2 text-xs font-normal text-ink-dim">yenileniyor…</span>}
          </h2>
          <button type="button" onClick={onClose} className="min-h-12 min-w-12 rounded text-ink-dim" aria-label="Kapat">
            ✕
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-4">
          {!checked ? (
            <p className="text-sm text-ink-dim">Yükleniyor…</p>
          ) : view === 'opening' ? (
            <div className="flex flex-col gap-4">
              <p className="text-base font-semibold text-ink">Kasada başlangıç parası ne kadar?</p>
              {!canOpenSession && (
                <p className="rounded-md border border-line bg-surface px-3 py-2 text-sm text-ink">
                  Bu istasyon şubeye bağlı değil — kasa açmak için şube bazlı oturum gerekli.
                </p>
              )}
              <div className="grid grid-cols-3 gap-2">
                {OPENING_PRESETS.map((preset) => (
                  <button
                    key={preset.input}
                    type="button"
                    onClick={() => {
                      setOpeningInput(preset.input)
                      setOpeningIsPreset(true)
                    }}
                    className="min-h-14 rounded-md border border-line bg-surface font-semibold text-ink"
                  >
                    {preset.label}
                  </button>
                ))}
              </div>
              <AmountDisplay label="Açılış sayımı" value={openingInput} />
              <Numpad
                mode="money"
                value={openingInput}
                pendingReplace={openingIsPreset}
                onChange={(next) => {
                  setOpeningInput(next)
                  setOpeningIsPreset(false)
                }}
              />
              {openingNotesShown ? (
                <label className="flex flex-col gap-1 text-sm text-ink">
                  Not (opsiyonel)
                  <textarea
                    value={openingNotes}
                    onChange={(e) => setOpeningNotes(e.target.value)}
                    rows={2}
                    className="rounded-md border border-line bg-surface px-3 py-2 text-sm text-ink"
                  />
                </label>
              ) : (
                <button
                  type="button"
                  onClick={() => setOpeningNotesShown(true)}
                  className="min-h-12 self-start rounded-md px-3 text-sm text-ink-dim underline-offset-2 hover:underline"
                >
                  + Not ekle
                </button>
              )}
              <button
                type="button"
                disabled={!canOpenSession || openingAmount < 0 || openingSubmitting}
                onClick={handleOpenSubmit}
                className="min-h-14 w-full rounded-lg bg-amber px-4 font-display text-lg font-bold text-amber-ink disabled:opacity-40"
              >
                {openingSubmitting ? 'Açılıyor…' : 'Kasayı Aç'}
              </button>
            </div>
          ) : view === 'status' && session ? (
            <div className="flex flex-col gap-4">
              <dl className="grid grid-cols-2 gap-y-2 text-sm">
                <dt className="text-ink-dim">Açılış sayımı</dt>
                <dd className="text-right tabular-nums text-ink">{formatMoney(session.opening_counted_amount)}</dd>
                <dt className="text-ink-dim">Alınan nakit</dt>
                <dd className="text-right tabular-nums text-ink">{formatMoney(session.cash_payments_taken)}</dd>
                <dt className="text-ink-dim">Kasa hareketleri (net)</dt>
                <dd className="text-right tabular-nums text-ink">{formatMoney(session.movements_net)}</dd>
                <dt className="font-semibold text-ink">Beklenen kapanış</dt>
                <dd className="text-right font-semibold tabular-nums text-ink">{formatMoney(session.expected_close)}</dd>
              </dl>
              <button
                type="button"
                onClick={() => setView('counting')}
                className="min-h-14 w-full rounded-lg bg-amber font-semibold text-amber-ink"
              >
                Sayımı Başlat
              </button>
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => setView('movement')}
                  className="min-h-14 flex-1 rounded-md border border-line font-semibold text-ink"
                >
                  Nakit Giriş/Çıkış
                </button>
                <button
                  type="button"
                  onClick={handleOpenLedger}
                  className="min-h-14 flex-1 rounded-md border border-line font-semibold text-ink"
                >
                  Hareket Defteri
                </button>
              </div>
            </div>
          ) : view === 'ledger' && session ? (
            <div className="flex flex-col gap-4">
              {movementsLoading ? (
                <p className="text-sm text-ink-dim">Yükleniyor…</p>
              ) : movements.length === 0 ? (
                <p className="text-sm text-ink-dim">Bu oturumda henüz nakit giriş/çıkış hareketi yok.</p>
              ) : (
                <ul className="flex flex-col gap-2">
                  {movements.map((m) => (
                    <li
                      key={m.id}
                      className="flex items-center justify-between gap-2 rounded-md border border-line px-3 py-2 text-sm"
                    >
                      <div className="flex flex-col">
                        <span className={m.direction === 'in' ? 'font-semibold text-teal' : 'font-semibold text-amber'}>
                          {m.direction === 'in' ? 'Giriş' : 'Çıkış'}
                        </span>
                        <span className="text-ink-dim">{m.reason}</span>
                      </div>
                      <div className="flex flex-col items-end">
                        <span className="tabular-nums text-ink">{formatMoney(m.amount_minor)}</span>
                        <span className="text-xs text-ink-dim">{new Date(m.created_at).toLocaleTimeString('tr-TR')}</span>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
              <button
                type="button"
                onClick={() => setView('status')}
                className="min-h-14 w-full rounded-md border border-line font-semibold text-ink"
              >
                Geri
              </button>
            </div>
          ) : view === 'movement' && session ? (
            // Two columns: amount + pad on the left, reason + actions on the
            // right, so on a 768px kiosk the save button never sits below the
            // fold of a tall single column.
            <div className="grid gap-4 md:grid-cols-2">
              <div className="flex flex-col gap-4">
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => changeDirection('in')}
                  className={`min-h-14 flex-1 rounded-md border font-semibold ${
                    direction === 'in' ? 'border-teal bg-teal/15 text-teal' : 'border-line text-ink'
                  }`}
                >
                  Giriş (kasaya para koy)
                </button>
                <button
                  type="button"
                  onClick={() => changeDirection('out')}
                  className={`min-h-14 flex-1 rounded-md border font-semibold ${
                    direction === 'out' ? 'border-amber bg-amber/15 text-amber' : 'border-line text-ink'
                  }`}
                >
                  Çıkış (kasadan para al)
                </button>
              </div>
              <AmountDisplay label="Tutar" value={movementInput} />
              <Numpad mode="money" value={movementInput} onChange={setMovementInput} />
              </div>
              <div className="flex flex-col justify-between gap-4">
              <div>
                <p className="mb-2 text-sm text-ink">Açıklama</p>
                <div className="flex flex-wrap gap-2">
                  {MOVEMENT_REASONS[direction].map((reason) => (
                    <button
                      key={reason}
                      type="button"
                      aria-pressed={!movementCustomReason && movementReason === reason}
                      onClick={() => {
                        setMovementCustomReason(false)
                        setMovementReason(reason)
                      }}
                      className={`min-h-12 rounded-md border px-3 text-sm font-semibold ${
                        !movementCustomReason && movementReason === reason
                          ? 'border-amber bg-amber/15 text-amber'
                          : 'border-line text-ink'
                      }`}
                    >
                      {reason}
                    </button>
                  ))}
                  <button
                    type="button"
                    aria-pressed={movementCustomReason}
                    onClick={() => {
                      setMovementCustomReason(true)
                      setMovementReason('')
                    }}
                    className={`min-h-12 rounded-md border px-3 text-sm ${
                      movementCustomReason ? 'border-amber text-amber' : 'border-dashed border-line text-ink-dim'
                    }`}
                  >
                    ✎ Başka açıklama
                  </button>
                </div>
                {movementCustomReason && (
                  <input
                    type="text"
                    value={movementReason}
                    onChange={(e) => setMovementReason(e.target.value)}
                    placeholder="Açıklama yazın"
                    aria-label="Açıklama"
                    className="mt-2 min-h-14 w-full rounded-md border border-line bg-surface px-3 text-ink"
                  />
                )}
              </div>
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => setView('status')}
                  className="min-h-14 flex-1 rounded-md border border-line font-semibold text-ink"
                >
                  Geri
                </button>
                <button
                  type="button"
                  disabled={movementAmount <= 0 || movementReason.trim() === '' || movementSubmitting}
                  onClick={handleMovementSubmit}
                  className="min-h-14 flex-1 rounded-lg bg-amber font-semibold text-amber-ink disabled:opacity-40"
                >
                  {movementSubmitting ? 'Kaydediliyor…' : 'Kaydet'}
                </button>
              </div>
              </div>
            </div>
          ) : view === 'counting' && session ? (
            <div className="flex flex-col gap-4">
              <p className="text-sm text-ink-dim">
                Çekmecedeki her kupürü sayıp giriniz — toplam otomatik hesaplanır.
              </p>
              <DenominationCounter rows={rows} onChange={setRows} disabled={countingSubmitting} />
              {countingNotesShown ? (
                <label className="flex flex-col gap-1 text-sm text-ink">
                  Not (opsiyonel)
                  <textarea
                    value={countingNotes}
                    onChange={(e) => setCountingNotes(e.target.value)}
                    rows={2}
                    className="rounded-md border border-line bg-surface px-3 py-2 text-sm text-ink"
                  />
                </label>
              ) : (
                <button
                  type="button"
                  onClick={() => setCountingNotesShown(true)}
                  className="min-h-12 self-start rounded-md px-3 text-sm text-ink-dim underline-offset-2 hover:underline"
                >
                  + Not ekle
                </button>
              )}
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => setView(session.status === 'closing_control' ? 'closing' : 'status')}
                  className="min-h-14 flex-1 rounded-md border border-line font-semibold text-ink"
                >
                  Geri
                </button>
                <button
                  type="button"
                  disabled={countingSubmitting}
                  onClick={handleCountingSubmit}
                  className="min-h-14 flex-1 rounded-lg bg-amber font-semibold text-amber-ink disabled:opacity-40"
                >
                  {countingSubmitting ? 'Gönderiliyor…' : 'Sayımı Gönder'}
                </button>
              </div>
            </div>
          ) : view === 'closing' && session && closingSnapshot ? (
            <div className="flex flex-col gap-4">
              {/*
                Only the FROZEN snapshot is ever rendered here — never
                session.expected_close/difference directly. That live figure
                exists solely to feed `stale` (see useCashSession.ts); showing
                it would be exactly the silent-drift bug this screen exists to
                prevent (task brief).
              */}
              <dl className="grid grid-cols-2 gap-y-2 text-sm">
                <dt className="text-ink-dim">Sayılan</dt>
                <dd className="text-right tabular-nums text-ink">{formatMoney(closingSnapshot.closingCountedAmount)}</dd>
                <dt className="text-ink-dim">Beklenen kapanış</dt>
                <dd className="text-right tabular-nums text-ink">{formatMoney(closingSnapshot.expectedClose)}</dd>
                <dt className="font-semibold text-ink">Fark</dt>
                <dd className="text-right font-semibold tabular-nums text-ink">
                  {formatMoney(computeDifference(closingSnapshot.closingCountedAmount, closingSnapshot.expectedClose))}
                </dd>
              </dl>

              {stale && (
                <div className="rounded-md border border-warn bg-warn/10 px-3 py-2 text-sm text-ink" role="alert">
                  Bu sayım bayatladı — kasa bakiyesi sayımdan sonra değişti (ör. bekleyen bir mali işlem
                  sonuçlandı). Yukarıdaki fark artık geçerli değil; kasayı yeniden sayın.
                </div>
              )}

              {cannotCloseReasons && cannotCloseReasons.length > 0 && (
                <div className="rounded-md border border-warn bg-warn/10 px-3 py-2 text-sm text-ink" role="alert">
                  <p className="font-semibold">Kasa kapatılamıyor:</p>
                  <ul className="mt-1 list-disc pl-5">
                    {cannotCloseReasons.map((reason) => (
                      <li key={reason}>{reason}</li>
                    ))}
                  </ul>
                  <div className="mt-2 flex gap-2">
                    <button
                      type="button"
                      onClick={handleRefresh}
                      disabled={refreshing}
                      className="min-h-12 rounded border border-line px-3 text-sm font-semibold text-ink"
                    >
                      {refreshing ? 'Yenileniyor…' : 'Durumu yenile'}
                    </button>
                    <button
                      type="button"
                      onClick={onDismissCannotClose}
                      className="min-h-12 rounded border border-line px-3 text-sm font-semibold text-ink"
                    >
                      Anladım
                    </button>
                  </div>
                </div>
              )}

              <button
                type="button"
                onClick={() => setView('counting')}
                className="min-h-14 w-full rounded-md border border-line font-semibold text-ink"
              >
                Yeniden Say
              </button>

              {!stale && !cannotCloseReasons && (
                <HoldButton
                  label="Basılı tutup kasayı kapat"
                  holdingLabel="Kapatılıyor…"
                  disabled={closeSubmitting}
                  onConfirm={handleCloseSubmit}
                />
              )}
            </div>
          ) : null}

          <div className="mt-4">
            <ErrorBanner message={error} />
          </div>
        </div>
      </div>
    </div>
  )
}
