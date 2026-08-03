import { useEffect, useState } from 'react'
import {
  canSwitchTo,
  isValidPinFormat,
  lockedRemedyMessage,
  noPinRemedyMessage,
  pinsMatch,
  type ParticipantView,
} from '../lib/cashierSwitch'
import { ErrorBanner } from './ErrorBanner'

type View = 'switch' | 'enterPin' | 'join'

type CashierSwitchModalProps = {
  open: boolean
  onClose: () => void
  participants: ParticipantView[]
  loading: boolean
  error: string
  onJoin: (pin: string) => Promise<boolean>
  /** Resolves true when the switch succeeded — App.tsx has already installed
   * the new session by the time this resolves (see useCashierSwitch's
   * switchTo doc comment); this component never sees the resulting session
   * itself, only whether to close. */
  onSwitch: (personId: string, pin: string) => Promise<boolean>
  onClearError: () => void
}

/**
 * ADR-DATA-008 "PIN akışının ayrıntıları" — kasiyer değiştir screen: pick a
 * name then enter a PIN (never a PIN-only lookup — see canSwitchTo's doc
 * comment), or join the session and set your own PIN. Two flows, one modal,
 * mirroring CashSessionModal's single-file/view-switch shape.
 *
 * HARD REQUIREMENT this component exists to uphold: the PIN never leaves
 * the two local `useState<string>` fields below except inside the one
 * onJoin/onSwitch call it is passed to. It is never logged, never compared
 * client-side (canSwitchTo/isValidPinFormat/pinsMatch check SHAPE and
 * MATCHING-EACH-OTHER only — never correctness), and is cleared from state
 * immediately after every submit attempt, success or failure.
 */
export function CashierSwitchModal({
  open,
  onClose,
  participants,
  loading,
  error,
  onJoin,
  onSwitch,
  onClearError,
}: CashierSwitchModalProps) {
  const [view, setView] = useState<View>('switch')
  const [selected, setSelected] = useState<ParticipantView | null>(null)
  const [pin, setPin] = useState('')
  const [switchSubmitting, setSwitchSubmitting] = useState(false)

  const [joinPin, setJoinPin] = useState('')
  const [joinPinConfirm, setJoinPinConfirm] = useState('')
  const [joinSubmitting, setJoinSubmitting] = useState(false)

  // Every field this modal owns resets to a blank slate on open/close — in
  // particular the pin fields, so a PIN typed in a previous visit can never
  // linger and get resubmitted against a different person by accident.
  useEffect(() => {
    setView('switch')
    setSelected(null)
    setPin('')
    setJoinPin('')
    setJoinPinConfirm('')
    onClearError()
    // onClearError is stable (useCallback in the owning hook), so omitting
    // it from deps here is deliberate: this effect must fire on `open`
    // transitions only, not merely because a parent re-render handed down a
    // new callback identity.
  }, [open])

  if (!open) return null

  function pickParticipant(p: ParticipantView) {
    if (!canSwitchTo(p)) return
    onClearError()
    setSelected(p)
    setPin('')
    setView('enterPin')
  }

  async function handleSwitchSubmit() {
    if (!selected) return
    setSwitchSubmitting(true)
    const ok = await onSwitch(selected.personId, pin)
    setSwitchSubmitting(false)
    // Cleared unconditionally — success or failure, the typed PIN has no
    // further reason to exist in this component's state (see the
    // file-level doc comment's hard requirement).
    setPin('')
    if (ok) {
      onClose()
    }
    // On failure, stay on the enterPin view (same selected participant) so
    // the cashier can retry without re-picking the name — `error` (from the
    // owning hook) is rendered below, generic by design.
  }

  async function handleJoinSubmit() {
    setJoinSubmitting(true)
    const ok = await onJoin(joinPin)
    setJoinSubmitting(false)
    setJoinPin('')
    setJoinPinConfirm('')
    if (ok) setView('switch')
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
      <div className="flex max-h-[90vh] w-full max-w-md flex-col overflow-hidden rounded-lg border border-line bg-panel">
        <div className="flex shrink-0 items-center justify-between border-b border-line px-4 py-3">
          <h2 className="font-display text-lg font-bold text-ink">Kasiyer Değiştir</h2>
          <button type="button" onClick={onClose} className="min-h-10 min-w-10 rounded text-ink-dim" aria-label="Kapat">
            ✕
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-4">
          {view === 'switch' && (
            <div className="flex flex-col gap-3">
              <p className="text-sm text-ink-dim">Vardiyaya katılmış bir isim seçin, ardından PIN girin.</p>

              {loading ? (
                <p className="text-sm text-ink-dim">Yükleniyor…</p>
              ) : participants.length === 0 ? (
                <p className="text-sm text-ink-dim">Bu oturuma henüz kimse katılmadı.</p>
              ) : (
                <ul className="flex flex-col gap-2">
                  {participants.map((p) => {
                    const selectable = canSwitchTo(p)
                    return (
                      <li key={p.personId}>
                        <button
                          type="button"
                          disabled={!selectable}
                          onClick={() => pickParticipant(p)}
                          className="flex min-h-14 w-full flex-col items-start justify-center gap-0.5 rounded-md border border-line px-3 py-2 text-left disabled:opacity-40"
                        >
                          <span className="font-semibold text-ink">{p.fullName}</span>
                          {!p.hasPin && <span className="text-xs text-ink-dim">{noPinRemedyMessage()}</span>}
                          {p.locked && <span className="text-xs text-ink-dim">{lockedRemedyMessage()}</span>}
                        </button>
                      </li>
                    )
                  })}
                </ul>
              )}

              <button
                type="button"
                onClick={() => setView('join')}
                className="min-h-14 w-full rounded-md border border-line font-semibold text-ink"
              >
                Vardiyaya Katıl / PIN Belirle
              </button>
            </div>
          )}

          {view === 'enterPin' && selected && (
            <div className="flex flex-col gap-4">
              <p className="text-sm text-ink">
                <span className="font-semibold">{selected.fullName}</span> için PIN girin.
              </p>
              <input
                type="password"
                inputMode="numeric"
                autoComplete="off"
                autoFocus
                value={pin}
                onChange={(e) => setPin(e.target.value.replace(/\D/g, '').slice(0, 6))}
                placeholder="PIN"
                className="min-h-14 rounded-md border border-line bg-surface px-3 text-center text-lg tracking-[0.3em] text-ink"
              />
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => {
                    onClearError()
                    setPin('')
                    setView('switch')
                  }}
                  className="min-h-14 flex-1 rounded-md border border-line font-semibold text-ink"
                >
                  Geri
                </button>
                <button
                  type="button"
                  disabled={!isValidPinFormat(pin) || switchSubmitting}
                  onClick={handleSwitchSubmit}
                  className="min-h-14 flex-1 rounded-lg bg-amber font-semibold text-amber-ink disabled:opacity-40"
                >
                  {switchSubmitting ? 'Doğrulanıyor…' : 'Onayla'}
                </button>
              </div>
            </div>
          )}

          {view === 'join' && (
            <div className="flex flex-col gap-4">
              <p className="text-sm text-ink-dim">
                Vardiyaya katılmak için PIN belirleyin (4-6 hane). PIN&apos;inizi yalnızca siz belirleyebilirsiniz —
                yönetici sizin adınıza PIN giremez.
              </p>
              <input
                type="password"
                inputMode="numeric"
                autoComplete="off"
                autoFocus
                value={joinPin}
                onChange={(e) => setJoinPin(e.target.value.replace(/\D/g, '').slice(0, 6))}
                placeholder="Yeni PIN"
                className="min-h-14 rounded-md border border-line bg-surface px-3 text-center text-lg tracking-[0.3em] text-ink"
              />
              <input
                type="password"
                inputMode="numeric"
                autoComplete="off"
                value={joinPinConfirm}
                onChange={(e) => setJoinPinConfirm(e.target.value.replace(/\D/g, '').slice(0, 6))}
                placeholder="PIN (tekrar)"
                className="min-h-14 rounded-md border border-line bg-surface px-3 text-center text-lg tracking-[0.3em] text-ink"
              />
              {joinPin !== '' && joinPinConfirm !== '' && !pinsMatch(joinPin, joinPinConfirm) && (
                <p className="text-sm text-ink">PIN&apos;ler eşleşmiyor.</p>
              )}
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => {
                    onClearError()
                    setJoinPin('')
                    setJoinPinConfirm('')
                    setView('switch')
                  }}
                  className="min-h-14 flex-1 rounded-md border border-line font-semibold text-ink"
                >
                  Geri
                </button>
                <button
                  type="button"
                  disabled={!isValidPinFormat(joinPin) || !pinsMatch(joinPin, joinPinConfirm) || joinSubmitting}
                  onClick={handleJoinSubmit}
                  className="min-h-14 flex-1 rounded-lg bg-amber font-semibold text-amber-ink disabled:opacity-40"
                >
                  {joinSubmitting ? 'Katılıyor…' : 'Katıl'}
                </button>
              </div>
            </div>
          )}

          <div className="mt-4">
            <ErrorBanner message={error} />
          </div>
        </div>
      </div>
    </div>
  )
}
