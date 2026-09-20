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

function conflict(status: number, code: string): string {
  return `apiclient: merge checks: apiclient: unexpected status ${status}: {"error":"x","code":"${code}"}`
}

// docs/pos-ux-spec.md §3c — the codes transfer / merge / move-items answer with.
// Each needs its own cashier-facing sentence: a bare 409 cannot tell "table is
// taken" from "this adisyon has payments" from "already closed".
describe('describeError — check moves', () => {
  it.each([
    { code: 'check_not_open', status: 409, want: 'Bu adisyon artık açık değil — liste yenilendi, işlemi yeniden başlatın.' },
    { code: 'check_branch_mismatch', status: 409, want: 'Bu adisyon başka bir şubeye ait — bu istasyondan işlem yapılamaz.' },
    { code: 'payments_present', status: 409, want: 'Ödemesi alınmış adisyon birleştirilemez — önce ödemesi olan adisyonu kapatın.' },
    { code: 'item_already_paid', status: 409, want: 'Seçilen kalemler için ödeme alınmış — ödenmiş kalemler taşınamaz.' },
    { code: 'same_check', status: 422, want: 'Kaynak ve hedef aynı adisyon — farklı bir masa seçin.' },
    { code: 'order_item_not_found', status: 422, want: 'Seçilen kalem bu adisyonda bulunamadı — adisyonu yenileyip tekrar seçin.' },
    { code: 'table_not_found', status: 422, want: 'Masa bulunamadı — masa planı yenilendi, tekrar seçin.' },
  ])('$code -> a specific message', ({ code, status, want }) => {
    expect(describeError(conflict(status, code))).toBe(want)
  })

  it('a table that already holds an adisyon points at the merge action', () => {
    expect(describeError(conflict(409, 'table_occupied'))).toContain('Bu masada açık adisyon var')
    expect(describeError(conflict(409, 'table_occupied'))).toContain('Masaları birleştir')
  })

  it('recognises every new code as machine-readable', () => {
    for (const code of ['check_not_open', 'check_branch_mismatch', 'payments_present', 'item_already_paid', 'same_check', 'order_item_not_found', 'table_not_found']) {
      expect(errorCode(conflict(409, code))).toBe(code)
    }
  })
})

// The server now checks prices on POST /pos/orders (P0): these two are new ways
// for a send to fail, and "çakışıyor" would send the cashier the wrong way.
describe('describeError — order validation', () => {
  it('explains a price mismatch', () => {
    expect(describeError(conflict(422, 'price_mismatch'))).toBe(
      'Bir ürünün fiyatı değişmiş — satırları kaldırıp ürünleri yeniden ekleyin.',
    )
  })

  it('explains an invalid order line', () => {
    expect(describeError(conflict(422, 'invalid_order_line'))).toBe('Sipariş satırlarından biri geçersiz — satırları kontrol edin.')
  })
})
