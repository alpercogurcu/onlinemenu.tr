// Visible-copy contract of the payment screen (approved payment design). The
// suite runs in the node environment (no jsdom — see vitest.config.ts), so the
// strings the cashier must be able to rely on are exported as constants and
// pinned here; the chip/label logic itself lives in lib/seatTotals.ts with its
// own tests.
import { describe, expect, it } from 'vitest'
import { CARD_DEVICE_NOTICE, PAID_ITEM_LABEL, SEAT_TAP_HINT } from './PaymentScreen'

describe('payment screen copy', () => {
  it('keeps the permanent card-device notice under the Kart action', () => {
    expect(CARD_DEVICE_NOTICE).toBe(
      'Kart: cihaz kayıtlı değil — tahsilat harici cihazda yapılır, buraya yalnız kayıt düşer.',
    )
  })

  it('explains what tapping a seat chip does', () => {
    expect(SEAT_TAP_HINT).toBe('Kişiye dokun — tutarı aşağıya yazar. Kayıt normal kısmi ödemedir.')
  })

  it('labels a settled item row in place of its amount', () => {
    expect(PAID_ITEM_LABEL).toBe('Ödendi')
  })
})
