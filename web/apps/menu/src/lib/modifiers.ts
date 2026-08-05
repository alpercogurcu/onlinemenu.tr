import type { MenuModifier, MenuModifierGroup } from "@/types/storefront"

export type ModifierSelection = Record<string, string[]>

/**
 * How many selections a group accepts.
 *
 * A "single" group accepts exactly one REGARDLESS of max_select: the server
 * enforces that even when max_select is 0 (WP2 contract §2), so a checkbox UI
 * here would produce a 422 the diner cannot act on. max_select 0 means
 * "unbounded" only for "multiple" groups.
 */
// The API rejects a line carrying more than 20 modifiers
// (storefront/http/public_handler.go maxLineModifiers). An unbounded
// "multiple" group with more options than that would otherwise let the UI
// build a cart the server refuses — the same "client allows what server
// rejects" gap that maxSelections closes for "single" groups.
export const MAX_LINE_MODIFIERS = 20

export function maxSelections(group: MenuModifierGroup): number {
  if (group.selection_type === "single") return 1
  return group.max_select > 0
    ? Math.min(group.max_select, MAX_LINE_MODIFIERS)
    : MAX_LINE_MODIFIERS
}

/**
 * min_select is NOT enforced by the server — deliberately, so a stale catalog
 * configuration cannot block ordering entirely (WP2 contract, "bilinçli
 * sınırlar"). That makes required-group validation this client's job, and the
 * reason it exists is worth stating where it is implemented.
 */
export function unsatisfiedRequiredGroups(
  groups: MenuModifierGroup[],
  selection: ModifierSelection,
): MenuModifierGroup[] {
  return groups.filter((group) => {
    const chosen = selection[group.id]?.length ?? 0
    return chosen < group.min_select
  })
}

export function toggleModifier(
  selection: ModifierSelection,
  group: MenuModifierGroup,
  modifierId: string,
): ModifierSelection {
  const current = selection[group.id] ?? []
  const isSelected = current.includes(modifierId)

  if (group.selection_type === "single") {
    // Radio semantics: re-tapping the chosen option clears it only when the
    // group is optional, so a min_select=1 group can never end up empty by
    // accident.
    if (isSelected) {
      return { ...selection, [group.id]: group.min_select > 0 ? current : [] }
    }
    return { ...selection, [group.id]: [modifierId] }
  }

  if (isSelected) {
    return { ...selection, [group.id]: current.filter((id) => id !== modifierId) }
  }
  if (current.length >= maxSelections(group)) return selection
  return { ...selection, [group.id]: [...current, modifierId] }
}

/** Flattens a selection into the modifier objects, in menu order. */
export function selectedModifiers(
  groups: MenuModifierGroup[],
  selection: ModifierSelection,
): MenuModifier[] {
  const out: MenuModifier[] = []
  for (const group of groups) {
    const chosen = selection[group.id] ?? []
    for (const modifier of group.modifiers) {
      if (chosen.includes(modifier.id)) out.push(modifier)
    }
  }
  return out
}

export function selectionPriceDelta(
  groups: MenuModifierGroup[],
  selection: ModifierSelection,
): number {
  return selectedModifiers(groups, selection).reduce((sum, m) => sum + m.price_delta, 0)
}

/** Pre-selects the first option of every required single-choice group. */
export function defaultSelection(groups: MenuModifierGroup[]): ModifierSelection {
  const selection: ModifierSelection = {}
  for (const group of groups) {
    const shouldPreselect =
      group.selection_type === "single" && group.min_select > 0 && group.modifiers.length > 0
    selection[group.id] = shouldPreselect ? [group.modifiers[0].id] : []
  }
  return selection
}
