// Pure rules for the option picker (docs/pos-ux-spec.md §3a): which groups are
// required, what the fast path pre-selects, how a tap changes a selection, and
// how the chosen options become a price and a kitchen-readable note. Kept out
// of the component so the rules are testable without a DOM, and declared on
// narrow local wire types (not the generated, gitignored wailsjs models) for
// the same reason as lib/branchFiscal.ts.

export type ModifierSource = {
  id: string
  name: string
  /** Signed price difference in kuruş. */
  price_delta: number
  sort_order?: number
}

export type ModifierGroupSource = {
  id: string
  name: string
  selection_type: string
  min_selections: number
  /** 0 means "no cap". */
  max_selections: number
  is_required: boolean
  sort_order?: number
  modifiers: ModifierSource[]
}

/** A chosen option, frozen with its name and price so the cart and receipt do not depend on the catalog staying loaded. */
export type SelectedModifier = {
  id: string
  groupId: string
  name: string
  priceDelta: number
}

/** groupId -> chosen modifier ids. */
export type OptionSelection = Record<string, string[]>

/** Ready-made kitchen notes; free text is the slow path on a keyboard-less kiosk. */
export const QUICK_NOTES = ['Soğansız', 'Az pişmiş', 'Çok pişmiş', 'Ayrı paket'] as const

const OPTION_NOTE_SEPARATOR = ' | '

function isSingle(group: ModifierGroupSource): boolean {
  return group.selection_type === 'single'
}

/** How many options the cashier must pick in this group before it counts as answered. */
export function requiredCount(group: ModifierGroupSource): number {
  const min = isSingle(group) ? 0 : group.min_selections
  return group.is_required ? Math.max(1, min) : min
}

/** Subtitle under a group's name, e.g. "zorunlu, tek seçim" / "opsiyonel, en fazla 2". */
export function groupHint(group: ModifierGroupSource): string {
  const need = requiredCount(group) > 0 ? 'zorunlu' : 'opsiyonel'
  if (isSingle(group)) return `${need}, tek seçim`
  return group.max_selections > 0 ? `${need}, en fazla ${group.max_selections}` : `${need}, çoklu seçim`
}

/** The two-tap fast path: every required group starts with its first option(s) chosen. */
export function defaultSelection(groups: readonly ModifierGroupSource[]): OptionSelection {
  const selection: OptionSelection = {}
  for (const group of groups) {
    selection[group.id] = group.modifiers.slice(0, requiredCount(group)).map((m) => m.id)
  }
  return selection
}

/**
 * Applies one tap. `blocked` is true when the tap was refused because the
 * group is at max_selections, so the picker can say why nothing happened.
 */
export function toggleModifier(
  group: ModifierGroupSource,
  current: readonly string[],
  modifierId: string,
): { next: string[]; blocked: boolean } {
  const selected = current.includes(modifierId)

  if (isSingle(group)) {
    if (selected) return { next: group.is_required ? [...current] : [], blocked: false }
    return { next: [modifierId], blocked: false }
  }

  if (selected) return { next: current.filter((id) => id !== modifierId), blocked: false }
  if (group.max_selections > 0 && current.length >= group.max_selections) {
    return { next: [...current], blocked: true }
  }
  return { next: [...current, modifierId], blocked: false }
}

/** Required groups that still need an answer. */
export function unmetGroupIds(groups: readonly ModifierGroupSource[], selection: OptionSelection): string[] {
  return groups.filter((g) => (selection[g.id]?.length ?? 0) < requiredCount(g)).map((g) => g.id)
}

/** Chosen options in display order (group order, then option order) — never tap order, so equal choices always hash and print equally. */
export function selectedModifiers(groups: readonly ModifierGroupSource[], selection: OptionSelection): SelectedModifier[] {
  const out: SelectedModifier[] = []
  for (const group of groups) {
    const chosen = selection[group.id] ?? []
    for (const modifier of group.modifiers) {
      if (chosen.includes(modifier.id)) {
        out.push({ id: modifier.id, groupId: group.id, name: modifier.name, priceDelta: modifier.price_delta })
      }
    }
  }
  return out
}

/** unit_price_amount = product price + the sum of the chosen price deltas. The server validates exactly this formula. */
export function unitPriceWith(basePriceAmount: number, modifiers: readonly SelectedModifier[]): number {
  return modifiers.reduce((sum, m) => sum + m.priceDelta, basePriceAmount)
}

function liraText(deltaKurus: number): string {
  const abs = Math.abs(deltaKurus)
  return abs % 100 === 0 ? String(abs / 100) : (abs / 100).toFixed(2).replace('.', ',')
}

/** Chip label such as "+₺15"; empty for a free option. */
export function formatDelta(deltaKurus: number): string {
  if (deltaKurus === 0) return ''
  return `${deltaKurus > 0 ? '+' : '−'}₺${liraText(deltaKurus)}`
}

function noteDelta(deltaKurus: number): string {
  if (deltaKurus === 0) return ''
  return `(${deltaKurus > 0 ? '+' : '-'}${liraText(deltaKurus)})`
}

/**
 * The order-item note: "Acılı | Lavaş(+5) | Soğansız". Options are stored in
 * `order_items.note` (no schema change), and the kitchen ticket already prints
 * the note under the item.
 */
export function composeOptionNote(modifiers: readonly SelectedModifier[], freeNote: string): string {
  const parts = modifiers.map((m) => `${m.name}${noteDelta(m.priceDelta)}`)
  const free = freeNote.trim()
  if (free) parts.push(free)
  return parts.join(OPTION_NOTE_SEPARATOR)
}

export function composeFreeNote(chips: readonly string[], custom: string): string {
  return [...chips, custom.trim()].filter(Boolean).join(', ')
}
