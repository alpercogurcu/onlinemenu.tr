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
import { Numpad } from './Numpad'

type View = 'switch' | 'join'

const PIN_MAX_LENGTH = 6

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
 * The kiosk has no keyboard, so the PIN is keyed on a Numpad. Picking a name
 * opens the pad beside the list on the same screen (docs/pos-ux-spec.md §3d —
 * no separate step). The PIN shows as dots and is never auto-submitted on the
 * 4th digit: a 4-digit PIN may be the prefix of a 6-digit one, and a premature
 * attempt would burn the wrong-PIN counter (ADR-DATA-008), so [Giriş] is an
 * explicit tap.
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
  const [joinField, setJoinField] = useState<'pin' | 'confirm'>('pin')
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
    setJoinField('pin')
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
  }

  function handleJoinKey(next: string) {
    if (joinField === 'pin') {
      setJoinPin(next)
      // Hand over to the confirmation field once the longest PIN is complete;
      // a 4-5 digit PIN needs the cashier to tap the second field themselves.
      if (next.length === PIN_MAX_LENGTH) setJoinField('confirm')
    } else {
      setJoinPinConfirm(next)
    }
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
    // On failure, stay on the same selected participant so the cashier can
    // retry without re-picking the name — `error` (from the owning hook) is
    // rendered below, generic by design.
  }

  async function handleJoinSubmit() {
    setJoinSubmitting(true)
    const ok = await onJoin(joinPin)
    setJoinSubmitting(false)
    setJoinPin('')
    setJoinPinConfirm('')
    setJoinField('pin')
    if (ok) setView('switch')
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
      <div className="flex max-h-[90vh] w-full max-w-3xl flex-col overflow-hidden rounded-lg border border-line bg-panel">
        <div className="flex shrink-0 items-center justify-between border-b border-line px-4 py-3">
          <h2 className="font-display text-lg font-bold text-ink">Kasiyer Değiştir</h2>
          <button type="button" onClick={onClose} className="min-h-12 min-w-12 rounded text-ink-dim" aria-label="Kapat">
            ✕
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-4">
          {view === 'switch' && (
            <div className="grid gap-4 md:grid-cols-2">
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
                      const isSelected = selected?.personId === p.personId
                      return (
                        <li key={p.personId}>
                          <button
                            type="button"
                            disabled={!selectable}
                            aria-pressed={isSelected}
                            onClick={() => pickParticipant(p)}
                            className={`flex min-h-14 w-full flex-col items-start justify-center gap-0.5 rounded-md border px-3 py-2 text-left disabled:opacity-40 ${
                              isSelected ? 'border-amber bg-surface' : 'border-line'
                            }`}
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

              <div className="flex flex-col gap-3">
                {selected ? (
                  <>
                    <p className="text-sm text-ink">
                      <span className="font-semibold">{selected.fullName}</span> için PIN girin.
                    </p>
                    <PinDots value={pin} label="PIN" />
                    <Numpad mode="pin" maxLength={PIN_MAX_LENGTH} value={pin} onChange={setPin} disabled={switchSubmitting} />
                    <button
                      type="button"
                      disabled={!isValidPinFormat(pin) || switchSubmitting}
                      onClick={handleSwitchSubmit}
                      className="min-h-14 w-full rounded-lg bg-amber font-semibold text-amber-ink disabled:opacity-40"
                    >
                      {switchSubmitting ? 'Doğrulanıyor…' : 'Giriş'}
                    </button>
                  </>
                ) : (
                  <p className="flex flex-1 items-center justify-center rounded-md border border-dashed border-line p-6 text-center text-sm text-ink-dim">
                    PIN girmek için soldan bir isim seçin.
                  </p>
                )}
              </div>
            </div>
          )}

          {view === 'join' && (
            <div className="grid gap-4 md:grid-cols-2">
              <div className="flex flex-col gap-3">
                <p className="text-sm text-ink-dim">
                  Vardiyaya katılmak için PIN belirleyin (4-6 hane). PIN&apos;inizi yalnızca siz belirleyebilirsiniz —
                  yönetici sizin adınıza PIN giremez.
                </p>
                <PinDots
                  value={joinPin}
                  label="Yeni PIN"
                  active={joinField === 'pin'}
                  onSelect={() => setJoinField('pin')}
                />
                <PinDots
                  value={joinPinConfirm}
                  label="PIN (tekrar)"
                  active={joinField === 'confirm'}
                  onSelect={() => setJoinField('confirm')}
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
                      setJoinField('pin')
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

              <Numpad
                mode="pin"
                maxLength={PIN_MAX_LENGTH}
                value={joinField === 'pin' ? joinPin : joinPinConfirm}
                onChange={handleJoinKey}
                disabled={joinSubmitting}
              />
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

/**
 * PIN readout: one dot per digit typed, never the digits themselves. When an
 * `onSelect` is given it is a tappable field (the join screen has two, and the
 * numpad edits whichever is active).
 */
function PinDots({
  value,
  label,
  active = false,
  onSelect,
}: {
  value: string
  label: string
  active?: boolean
  onSelect?: () => void
}) {
  const content = (
    <>
      <span className="text-sm text-ink-dim">{label}</span>
      <span className="flex items-center gap-2" aria-hidden="true">
        {Array.from({ length: PIN_MAX_LENGTH }, (_, i) => (
          <span
            key={i}
            className={`h-3.5 w-3.5 rounded-full border ${i < value.length ? 'border-ink bg-ink' : 'border-ink-dim'}`}
          />
        ))}
      </span>
    </>
  )
  const className = `flex min-h-14 items-center justify-between gap-3 rounded-md border bg-surface px-3 ${
    active ? 'border-amber' : 'border-line'
  }`
  const ariaLabel = `${label}, ${value.length} hane girildi`

  if (!onSelect) {
    return (
      <div role="status" aria-label={ariaLabel} className={className}>
        {content}
      </div>
    )
  }
  return (
    <button type="button" aria-pressed={active} aria-label={ariaLabel} onClick={onSelect} className={`${className} w-full`}>
      {content}
    </button>
  )
}
