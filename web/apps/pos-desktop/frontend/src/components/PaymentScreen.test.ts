// Visible-copy contract of the payment screen (approved payment design). The
// suite runs in the node environment (no jsdom — see vitest.config.ts), so the
// strings the cashier must be able to rely on are exported as constants and
// pinned here; the chip/label logic itself lives in lib/seatTotals.ts with its
// own tests.
import { describe, expect, it } from 'vitest'
import { PAID_ROW_LABEL } from '../lib/payRow'
import { CARD_DEVICE_NOTICE, SEAT_TAP_HINT, SELECT_FROM_RECEIPT_HINT, roundingButtonLabel, roundingScopeNote } from './PaymentScreen'

describe('payment screen copy', () => {
  it('keeps the permanent card-device notice under the Kart action', () => {
    expect(CARD_DEVICE_NOTICE).toBe(
      'Kart: cihaz kayıtlı değil — tahsilat harici cihazda yapılır, buraya yalnız kayıt düşer.',
    )
  })

  it('explains what tapping a seat chip does', () => {
    expect(SEAT_TAP_HINT).toBe(
      'Kişiye dokun — o kişinin ödenmemiş kalemleri seçilir. Birden çok kişi birlikte seçilebilir.',
    )
  })

  it('points the cashier at the receipt rail while nothing is picked', () => {
    expect(SELECT_FROM_RECEIPT_HINT).toBe('Sağdaki adisyondan kalem seçebilirsiniz.')
  })

  it('labels a settled item row on the rail', () => {
    expect(PAID_ROW_LABEL).toBe('Ödendi')
  })
})

describe('rounding button copy', () => {
  const offer = { rounded: 43500, rounding: 250 }
  const on = { cashEnabled: true, cardEnabled: true, stepMinor: 500, maxPerCheckMinor: 1000 }

  it('offers the rounded amount and the concession', () => {
    expect(roundingButtonLabel(offer, false)).toBe('↓ Yuvarla: ₺435,00 (−₺2,50)')
  })

  it('reads as undoable once applied', () => {
    expect(roundingButtonLabel(offer, true)).toBe('✓ Yuvarlandı: ₺435,00 (−₺2,50) — geri al')
  })

  it('says which method keeps the full amount', () => {
    expect(roundingScopeNote({ ...on, cardEnabled: false })).toBe('Yuvarlama yalnız nakitte — kartla tam tutar alınır.')
    expect(roundingScopeNote({ ...on, cashEnabled: false })).toBe('Yuvarlama yalnız kartta — nakitte tam tutar alınır.')
    expect(roundingScopeNote(on)).toBe('')
  })
})
