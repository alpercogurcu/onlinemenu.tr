import { useCallback, useEffect, useState } from 'react'
import { JoinCashSession, ListCashSessionParticipants, SwitchCashier } from '../../wailsjs/go/main/App'
import type { main } from '../../wailsjs/go/models'
import { describeError } from '../lib/errors'
import { toParticipantView, type ParticipantView } from '../lib/cashierSwitch'

export type UseCashierSwitchResult = {
  participants: ParticipantView[]
  loading: boolean
  /** Generic failure message (already Turkish via describeError) — for
   * SwitchCashier's 401 this is the deliberately generic "doğrulama
   * başarısız" message (see lib/errors.ts), never a specific reason. */
  error: string
  refresh: () => Promise<void>
  /** Joins sessionId as the CURRENT principal, optionally setting their own
   * pin in the same call. Resolves true on success (and refreshes the
   * participant list so the UI reflects it immediately). */
  join: (pin: string) => Promise<boolean>
  /**
   * Verifies pin against personId's stored pin on sessionId. On success,
   * returns the resulting main.SessionDTO (the new acting identity) — the
   * caller (App.tsx) is responsible for installing it as the app's current
   * session, mirroring how handleSelectContext consumes
   * SelectKeycloakContext's result. Returns null on any failure; `error`
   * carries the message.
   */
  switchTo: (personId: string, pin: string) => Promise<main.SessionDTO | null>
  clearError: () => void
  reset: () => void
}

/**
 * Owns the ADR-DATA-008 PIN akışı read/write calls for one cash session:
 * fetching the participant list, joining, and PIN-based switching. Mirrors
 * useCashSession's shape (loading/error/refresh + action callbacks) so the
 * two hooks read the same way side by side.
 *
 * sessionId should come from the branch's active cash session
 * (useCashSession's `session.id`). Undefined (no open session, or logged
 * out) means there is nothing to join or switch within — every action
 * becomes a no-op and `participants` stays empty, mirroring
 * useCashSession's own branchId-undefined behavior.
 */
export function useCashierSwitch(sessionId: string | undefined): UseCashierSwitchResult {
  const [participants, setParticipants] = useState<ParticipantView[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const reset = useCallback(() => {
    setParticipants([])
    setError('')
  }, [])

  // Render-time reset on session change — same pattern as
  // useCashSession's branchId-keyed reset: a cashier switch or a session
  // close must not leave the PREVIOUS session's participant list on screen
  // for whatever the new sessionId (or none) actually has.
  const [appliedSessionId, setAppliedSessionId] = useState(sessionId)
  if (appliedSessionId !== sessionId) {
    setAppliedSessionId(sessionId)
    setParticipants([])
    setError('')
  }

  const refresh = useCallback(async (): Promise<void> => {
    if (!sessionId) {
      setParticipants([])
      return
    }
    setLoading(true)
    try {
      const list = await ListCashSessionParticipants(sessionId)
      setParticipants(list.map(toParticipantView))
      setError('')
    } catch (err) {
      setError(describeError(err))
    } finally {
      setLoading(false)
    }
  }, [sessionId])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const join = useCallback(
    async (pin: string): Promise<boolean> => {
      if (!sessionId) return false
      setError('')
      try {
        await JoinCashSession(sessionId, pin)
        await refresh()
        return true
      } catch (err) {
        setError(describeError(err))
        return false
      }
    },
    [sessionId, refresh],
  )

  const switchTo = useCallback(
    async (personId: string, pin: string): Promise<main.SessionDTO | null> => {
      if (!sessionId) return null
      setError('')
      try {
        return await SwitchCashier(sessionId, personId, pin)
      } catch (err) {
        setError(describeError(err))
        return null
      }
    },
    [sessionId],
  )

  const clearError = useCallback(() => setError(''), [])

  return { participants, loading, error, refresh, join, switchTo, clearError, reset }
}
