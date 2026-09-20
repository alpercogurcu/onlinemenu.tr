import { useState, type ReactNode } from 'react'
import { limitBanners } from '../lib/banners'

export type Banner = { key: string; node: ReactNode }

const MAX_ROWS = 2

/**
 * The warning banners under the header, held to two rows (bulgu #11). Ordered
 * most important first by the caller; when more than fit, the rest fold into a
 * single "+N" row the cashier can expand — the warnings stay reachable, they
 * just stop eating a 768px screen.
 */
export function BannerStack({ banners }: { banners: Banner[] }) {
  const [expanded, setExpanded] = useState(false)
  const { visible, hidden } = limitBanners(banners, MAX_ROWS, expanded)

  return (
    <>
      {visible.map((b) => (
        <div key={b.key} className="contents">
          {b.node}
        </div>
      ))}
      {hidden > 0 && (
        <button
          type="button"
          onClick={() => setExpanded(true)}
          className="flex min-h-12 shrink-0 items-center justify-between gap-3 border-b border-line border-l-4 border-l-warn bg-warn/10 px-4 text-left text-sm text-ink"
        >
          <span>+{hidden} uyarı daha</span>
          <span className="font-semibold">Hepsini göster</span>
        </button>
      )}
      {expanded && banners.length > MAX_ROWS && (
        <button
          type="button"
          onClick={() => setExpanded(false)}
          className="flex min-h-12 shrink-0 items-center justify-end border-b border-line px-4 text-sm font-semibold text-ink-dim"
        >
          Uyarıları daralt
        </button>
      )}
    </>
  )
}
