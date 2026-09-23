// Rules and wording for the three adisyon actions that pick a target table:
// masayı taşı, masaları birleştir, kalem taşı (docs/pos-ux-spec.md §3c). Pure,
// so what may be tapped on the floor plan is testable without a DOM.

export type TargetKind = 'transfer' | 'merge' | 'move-items'

/** Narrow shape this module needs from a TableDTO — declared locally, see cart.ts's ProductSource doc comment. */
export type PlanTable = {
  name: string
  status: string
  active_check_id?: string
}

/** The backend's name for a free table (pos/domain.TableStatusEmpty) — NOT "available". */
const FREE_TABLE_STATUS = 'empty'

function holdsAnotherCheck(table: PlanTable, currentCheckId: string): boolean {
  return table.status === 'occupied' && Boolean(table.active_check_id) && table.active_check_id !== currentCheckId
}

/**
 * Whether a table can be the target. The rules mirror what the server accepts,
 * so a tap that would only produce an error is not offered:
 * - transfer: a free table (a table with an adisyon is what merge is for);
 * - merge: another adisyon — the table the check already sits on would be
 *   `same_check`;
 * - move-items: another adisyon, or a free table (the caller opens a new
 *   adisyon there first — the common "split the bill" case).
 * Reserved and cleaning tables are never offered.
 */
export function canPickTable(kind: TargetKind, table: PlanTable, currentCheckId: string): boolean {
  switch (kind) {
    case 'transfer':
      return table.status === FREE_TABLE_STATUS
    case 'merge':
      return holdsAnotherCheck(table, currentCheckId)
    case 'move-items':
      return holdsAnotherCheck(table, currentCheckId) || table.status === FREE_TABLE_STATUS
  }
}

export function targetPrompt(kind: TargetKind, sourceLabel: string): string {
  switch (kind) {
    case 'transfer':
      return `Hedef masayı seçin — ${sourceLabel} taşınacak`
    case 'merge':
      return `Hedef masayı seçin — ${sourceLabel} seçilen masanın adisyonuna eklenecek`
    case 'move-items':
      return 'Hedef masayı seçin — seçilen kalemler taşınacak'
  }
}

/** The fourth tap of a merge: it is the one step that cannot be undone, so it says who survives. */
export function confirmMerge(sourceLabel: string, targetLabel: string): { title: string; message: string } {
  return {
    title: 'Adisyonlar birleştirilsin mi?',
    message: `${sourceLabel} adisyonunun tüm kalemleri ${targetLabel} adisyonuna aktarılır ve ${sourceLabel} kapanır. Bu işlem geri alınamaz.`,
  }
}

export function moveNotice(count: number, targetLabel: string): string {
  return `${count} kalem ${targetLabel} adisyonuna taşındı.`
}
