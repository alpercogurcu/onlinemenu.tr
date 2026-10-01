"use client"

import { InfoIcon } from "lucide-react"
import { useTranslations } from "next-intl"

/**
 * Shown while the branch has paused QR ordering. Deliberately informational —
 * this is a business decision, not an error, so no destructive styling: the
 * diner keeps browsing and is pointed at the staff, not at a problem.
 */
export function OrderingClosedNotice() {
  const t = useTranslations("menu")

  return (
    <div role="status" className="bg-muted/50 flex items-start gap-3 rounded-xl border p-3">
      <InfoIcon className="text-muted-foreground mt-0.5 size-5 shrink-0" aria-hidden="true" />
      <div className="min-w-0">
        <p className="text-sm font-medium">{t("orderingClosedTitle")}</p>
        <p className="text-muted-foreground mt-0.5 text-sm">{t("orderingClosedHint")}</p>
      </div>
    </div>
  )
}
