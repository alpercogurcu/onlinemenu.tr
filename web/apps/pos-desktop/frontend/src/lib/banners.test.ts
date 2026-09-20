import { describe, expect, it } from 'vitest'
import { limitBanners } from './banners'

const rows = (n: number) => Array.from({ length: n }, (_, i) => `b${i}`)

describe('limitBanners', () => {
  it('shows everything that fits in two rows', () => {
    expect(limitBanners(rows(0), 2, false)).toEqual({ visible: [], hidden: 0 })
    expect(limitBanners(rows(1), 2, false)).toEqual({ visible: ['b0'], hidden: 0 })
    expect(limitBanners(rows(2), 2, false)).toEqual({ visible: ['b0', 'b1'], hidden: 0 })
  })

  it('keeps the most important banner and folds the rest into one "+N" row', () => {
    expect(limitBanners(rows(3), 2, false)).toEqual({ visible: ['b0'], hidden: 2 })
    expect(limitBanners(rows(7), 2, false)).toEqual({ visible: ['b0'], hidden: 6 })
  })

  it('never takes more than the row budget: visible banners plus the summary row', () => {
    for (let n = 0; n < 12; n++) {
      const { visible, hidden } = limitBanners(rows(n), 2, false)
      expect(visible.length + (hidden > 0 ? 1 : 0)).toBeLessThanOrEqual(2)
    }
  })

  it('shows all of them once the cashier expands the list', () => {
    expect(limitBanners(rows(5), 2, true)).toEqual({ visible: rows(5), hidden: 0 })
  })
})
