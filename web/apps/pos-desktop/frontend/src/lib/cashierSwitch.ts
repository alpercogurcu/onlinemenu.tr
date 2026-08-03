// ADR-DATA-008 "PIN akışının ayrıntıları" — client-side pure logic for the
// kasiyer değiştir (cashier join/switch) screen. Kept here (not in the
// component) for the same reason as lib/cashSession.ts: this is where a bug
// would actually live, and it is testable without the generated (gitignored)
// wailsjs bindings — see lib/branchFiscal.ts's file-level comment for why
// these wire types are hand-declared rather than imported from
// '../../wailsjs/go/models'.
//
// This file NEVER stores, compares, or evaluates an actual PIN against
// anything. isValidPinFormat only checks shape (length/digit-only) — the
// same kind of client-side format gate CashSessionModal's amount inputs use
// elsewhere — and pinsMatch only compares two strings the cashier just
// typed into the SAME form, to catch a typo before submit. Whether a PIN is
// actually correct is a server-only question by construction (ADR-DATA-008
// Karar 2 / PIN akışı §2): there is no PIN verification of any kind here.

/** Narrow wire shape this module needs from a ParticipantDTO-like value. */
export type ParticipantSource = {
  person_id: string
  full_name: string
  has_pin: boolean
  locked: boolean
}

export type ParticipantView = {
  personId: string
  fullName: string
  hasPin: boolean
  locked: boolean
}

export function toParticipantView(p: ParticipantSource): ParticipantView {
  return { personId: p.person_id, fullName: p.full_name, hasPin: p.has_pin, locked: p.locked }
}

/**
 * Whether a participant can be picked as the switch target right now.
 * ADR-DATA-008 PIN akışı §3: "a participant with has_pin: false cannot be
 * switched to" and a locked one is closed until re-join — both are gates on
 * the NAME being selectable at all, never on the PIN typed afterward (this
 * app never does a PIN-only lookup, see the ADR addendum).
 */
export function canSwitchTo(p: ParticipantView): boolean {
  return p.hasPin && !p.locked
}

/**
 * ADR-DATA-008 PIN akışı §5: the lock never expires on a timer — only a
 * full Keycloak re-join clears it. This message exists specifically so the
 * UI never implies "wait and try again", the anti-pattern the task brief
 * calls out by name.
 */
export function lockedRemedyMessage(): string {
  return 'PIN denemeleri kilitlendi. Kilidi yalnızca tam Keycloak girişiyle yeniden vardiyaya katılmak açar — beklemek yardımcı olmaz.'
}

/**
 * ADR-DATA-008 PIN akışı §3: a participant who has not set a PIN yet must be
 * guided to join via Keycloak, not offered a PIN field they cannot possibly
 * pass.
 */
export function noPinRemedyMessage(): string {
  return 'Bu kişi henüz PIN belirlememiş — vardiyaya Keycloak ile katılıp PIN belirlemeli.'
}

/**
 * Mirrors identity/domain.ValidatePinFormat's 4-6 digit rule exactly, so the
 * join/switch submit buttons can be disabled client-side instead of
 * round-tripping to a 422 (same rationale as cashSession.ts's
 * isValidMovementAmount). This is a FORMAT check only — it says nothing
 * about whether the pin is correct, which this client never evaluates.
 */
export function isValidPinFormat(pin: string): boolean {
  return /^[0-9]{4,6}$/.test(pin)
}

/**
 * The join screen asks the cashier to type their new pin twice, mirroring an
 * ordinary "set password" UX. This is the only client-side check on the
 * confirmation field — a typo catcher, not a verification of anything
 * server-side.
 */
export function pinsMatch(a: string, b: string): boolean {
  return a.length > 0 && a === b
}

/**
 * Finds the current principal's own row in the participant list, if they
 * have already joined this session. Used to decide whether the join screen
 * should read as "vardiyaya katıl" (first time) or "PIN'i yeniden belirle"
 * (already joined, re-joining refreshes the PIN — see backend Join's
 * upsert semantics).
 */
export function findOwnParticipant(
  participants: readonly ParticipantView[],
  personId: string | undefined,
): ParticipantView | null {
  if (!personId) return null
  return participants.find((p) => p.personId === personId) ?? null
}
