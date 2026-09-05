import { describe, expect, it } from 'vitest'
import { describeError, errorCode, isForbiddenError } from './errors'

// ADR-DATA-008 Join's 403 split (payment/http/cash_session_pin_handler.go):
// session_scoped_principal and branch_forbidden used to both surface as the
// same bare "status 403" text. Only session_scoped_principal gets a specific
// message here — branch_forbidden is left to the existing generic 403
// fallback, which already says the right thing ("yetkiniz yok").

function wailsError(status: number, code: string): string {
  return `apiclient: join cash session: apiclient: unexpected status ${status}: {"error":"forbidden","code":"${code}"}`
}

describe('errorCode', () => {
  it('extracts session_scoped_principal from a Join 403 body', () => {
    expect(errorCode(wailsError(403, 'session_scoped_principal'))).toBe('session_scoped_principal')
  })

  it('returns null for an unrecognized code (e.g. branch_forbidden)', () => {
    expect(errorCode(wailsError(403, 'branch_forbidden'))).toBeNull()
  })
})

describe('describeError — session_scoped_principal', () => {
  it('tells the cashier to re-authenticate via Keycloak', () => {
    expect(describeError(wailsError(403, 'session_scoped_principal'))).toBe(
      'Bu işlem için Keycloak ile yeniden giriş yapın.',
    )
  })

  it('falls back to the generic 403 message for branch_forbidden', () => {
    expect(describeError(wailsError(403, 'branch_forbidden'))).toBe(
      'Bu işlem için yetkiniz yok (şube/rol uyuşmazlığı).',
    )
  })

  it('falls back to the generic 403 message for a coded-less 403', () => {
    expect(describeError('apiclient: join cash session: apiclient: unexpected status 403: forbidden')).toBe(
      'Bu işlem için yetkiniz yok (şube/rol uyuşmazlığı).',
    )
  })
})

// Regression guard: the new code must not disturb isForbiddenError, which
// several call sites (e.g. the QR dialog) key off independently of describeError.
describe('isForbiddenError', () => {
  it('still reports true for a session_scoped_principal 403', () => {
    expect(isForbiddenError(wailsError(403, 'session_scoped_principal'))).toBe(true)
  })
})
