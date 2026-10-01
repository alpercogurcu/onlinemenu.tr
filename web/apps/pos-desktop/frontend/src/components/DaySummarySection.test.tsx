import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { formatMoney } from '@onlinemenu/pos-core'
import { DaySummarySection, deriveDaySummary, type SaleDetailsSource } from './DaySummarySection'

describe('deriveDaySummary', () => {
  it('splits completed payments into nakit (cash) and kart (terminal) totals', () => {
    const details: SaleDetailsSource = {
      sales: { closed_check_count: 18, gross: 1482000 },
      payments: [
        { method: 'cash', status: 'completed', total: 624000 },
        { method: 'terminal', status: 'completed', total: 858000 },
      ],
    }
    expect(deriveDaySummary(details)).toEqual({
      closedCheckCount: 18,
      gross: 1482000,
      cashTotal: 624000,
      cardTotal: 858000,
    })
  })

  it('counts only completed buckets — pending/failed/voided are not money in hand', () => {
    const details: SaleDetailsSource = {
      sales: { closed_check_count: 3, gross: 50000 },
      payments: [
        { method: 'cash', status: 'completed', total: 10000 },
        { method: 'cash', status: 'pending', total: 7000 },
        { method: 'cash', status: 'voided', total: 5000 },
        { method: 'terminal', status: 'failed', total: 9000 },
      ],
    }
    const summary = deriveDaySummary(details)
    expect(summary.cashTotal).toBe(10000)
    expect(summary.cardTotal).toBe(0)
  })

  it('leaves non-drawer methods (meal_card, comp, …) out of both tahsilat figures', () => {
    const details: SaleDetailsSource = {
      sales: { closed_check_count: 2, gross: 30000 },
      payments: [
        { method: 'meal_card', status: 'completed', total: 20000 },
        { method: 'comp', status: 'completed', total: 10000 },
      ],
    }
    const summary = deriveDaySummary(details)
    expect(summary.cashTotal).toBe(0)
    expect(summary.cardTotal).toBe(0)
    expect(summary.gross).toBe(30000)
  })

  it('sums repeated (method, status) buckets instead of taking the last one', () => {
    const details: SaleDetailsSource = {
      sales: { closed_check_count: 4, gross: 40000 },
      payments: [
        { method: 'cash', status: 'completed', total: 15000 },
        { method: 'cash', status: 'completed', total: 25000 },
      ],
    }
    expect(deriveDaySummary(details).cashTotal).toBe(40000)
  })

  it('handles an empty report (no sales yet) without blowing up', () => {
    const summary = deriveDaySummary({ sales: { closed_check_count: 0, gross: 0 }, payments: [] })
    expect(summary).toEqual({ closedCheckCount: 0, gross: 0, cashTotal: 0, cardTotal: 0 })
  })
})

describe('DaySummarySection', () => {
  const summary = { closedCheckCount: 18, gross: 1482000, cashTotal: 624000, cardTotal: 858000 }
  const noop = () => undefined

  it('renders the four cards with formatted figures', () => {
    const html = renderToStaticMarkup(
      <DaySummarySection summary={summary} loading={false} error="" onRetry={noop} />,
    )
    expect(html).toContain('GÜN ÖZETİ')
    expect(html).toContain('Kapanan adisyon')
    expect(html).toContain('>18<')
    expect(html).toContain('Brüt satış')
    expect(html).toContain(formatMoney(1482000))
    expect(html).toContain('Nakit tahsilat')
    expect(html).toContain(formatMoney(624000))
    expect(html).toContain('Kart tahsilat')
    expect(html).toContain(formatMoney(858000))
  })

  it('shows a skeleton with the real labels while loading without data', () => {
    const html = renderToStaticMarkup(
      <DaySummarySection summary={null} loading error="" onRetry={noop} />,
    )
    expect(html).toContain('aria-busy="true"')
    expect(html).toContain('Kapanan adisyon')
    expect(html).toContain('animate-pulse')
    expect(html).not.toContain('Yeniden dene')
  })

  it('shows the failure and a retry instead of hiding silently (task brief)', () => {
    const html = renderToStaticMarkup(
      <DaySummarySection summary={null} loading={false} error="Sunucuya ulaşılamadı" onRetry={noop} />,
    )
    expect(html).toContain('role="alert"')
    expect(html).toContain('Gün özeti yüklenemedi: Sunucuya ulaşılamadı')
    expect(html).toContain('Yeniden dene')
    expect(html).not.toContain('animate-pulse')
  })

  it('prefers the error over stale data — a failed refresh must be visible', () => {
    const html = renderToStaticMarkup(
      <DaySummarySection summary={summary} loading={false} error="Oturum süresi doldu" onRetry={noop} />,
    )
    expect(html).toContain('Gün özeti yüklenemedi: Oturum süresi doldu')
    expect(html).not.toContain('Kapanan adisyon')
  })

  it('renders nothing but the heading before the first load kicks in', () => {
    const html = renderToStaticMarkup(
      <DaySummarySection summary={null} loading={false} error="" onRetry={noop} />,
    )
    expect(html).toContain('GÜN ÖZETİ')
    expect(html).not.toContain('Kapanan adisyon')
  })
})
