// Kitchen INFORMATION notices (docs/pos-ux-spec.md §3c): the slip that tells the
// cook an adisyon moved tables, two were merged, or items went elsewhere. The Go
// side prints it after the move; if the printer fails the move stands and the
// failure stays visible here until it is reprinted or dismissed — the same rule
// as a kitchen ticket (lib/kitchenPrint), because a slip that never reached the
// kitchen means a dish goes to the wrong table.

/** Narrow shape of main.KitchenNoticeDTO — declared locally, see lib/branchFiscal.ts. */
export type KitchenNoticeSource = {
  kind: string
  from: string
  to: string
  items: { name: string; quantity: number }[]
}

export type NoticeFailure = {
  id: string
  /** Null when the notice could not even be prepared (nothing to reprint). */
  notice: KitchenNoticeSource | null
  message: string
}

export function addNoticeFailure(failures: readonly NoticeFailure[], failure: NoticeFailure): NoticeFailure[] {
  return [...removeNoticeFailure(failures, failure.id), failure]
}

export function removeNoticeFailure(failures: readonly NoticeFailure[], id: string): NoticeFailure[] {
  return failures.filter((f) => f.id !== id)
}

const KIND_LABEL: Record<string, string> = {
  transfer: 'Masa taşındı bilgi fişi',
  merge: 'Birleştirme bilgi fişi',
  'move-items': 'Kalem taşıma bilgi fişi',
}

export function describeNoticeFailure(failure: NoticeFailure): string {
  if (!failure.notice) return `Mutfak bilgi fişi hazırlanamadı: ${failure.message}`
  const label = KIND_LABEL[failure.notice.kind] ?? 'Mutfak bilgi fişi'
  return `${label} yazdırılamadı (${failure.notice.from} → ${failure.notice.to}): ${failure.message}`
}
