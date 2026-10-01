import { describe, expect, it } from 'vitest'
import { formatMoney } from '@onlinemenu/pos-core'
import { cashChipLabel, deviceBadges } from './statusBar'

describe('cashChipLabel', () => {
  it('shows the opening count', () => {
    expect(cashChipLabel({ status: 'open', opening_counted_amount: 200000 })).toBe(
      `KASA AÇIK · ${formatMoney(200000)} açılış`,
    )
  })

  it('flags a submitted closing count', () => {
    expect(cashChipLabel({ status: 'closing_control', opening_counted_amount: 200000 })).toBe(
      `KASA AÇIK · ${formatMoney(200000)} açılış · sayım gönderildi`,
    )
  })
})

describe('deviceBadges', () => {
  it('always carries the fixed ÖKC badge (Faz 2) and stays silent on connected printers', () => {
    const badges = deviceBadges({ status: 'connected' }, { status: 'connected' })
    expect(badges.map((b) => b.key)).toEqual(['okc'])
    expect(badges[0].label).toBe('ÖKC yok')
  })

  it('reports a disconnected and an errored printer separately', () => {
    const badges = deviceBadges({ status: 'disconnected' }, { status: 'error', error: 'kağıt yok' })
    expect(badges.map((b) => b.label)).toEqual(['Yazıcı yok', 'Mutfak yazıcısı hata', 'ÖKC yok'])
    expect(badges[1].detail).toBe('kağıt yok')
  })

  it('treats an unknown device (no status event yet) as nothing to report', () => {
    expect(deviceBadges(null, null).map((b) => b.key)).toEqual(['okc'])
  })
})
