// Caps the warning banners under the header at a fixed row budget
// (docs/pos-ux-spec.md bulgu #11): cash session + printer + kitchen(N) +
// fiscal(N) used to stack without limit and eat the working area of a 768px
// screen.

/**
 * `items` must already be ordered most important first. When they do not fit
 * in `maxRows`, the least important are folded into one summary row, so the
 * budget counts that row too: `maxRows - 1` banners stay visible and `hidden`
 * says how many the summary stands for. `expanded` lifts the cap (the cashier
 * asked to see them all).
 */
export function limitBanners<T>(items: readonly T[], maxRows: number, expanded: boolean): { visible: T[]; hidden: number } {
  if (expanded || items.length <= maxRows) return { visible: [...items], hidden: 0 }
  const keep = Math.max(0, maxRows - 1)
  return { visible: items.slice(0, keep), hidden: items.length - keep }
}
