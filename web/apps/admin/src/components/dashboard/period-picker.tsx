"use client"

import { useTranslations } from "next-intl"

import { Button } from "@/components/ui/button"
import type { ReportPeriod } from "@/hooks/use-reports"

const PERIODS: ReportPeriod[] = ["today", "yesterday", "last7", "thisMonth"]

// No shadcn ToggleGroup is installed in this app (see components/ui listing),
// so the four presets are a row of Buttons instead — the selected one styled
// as `default`, the rest as `outline`. Selection is lifted to the parent (the
// dashboard owns `period` state) so it can drive useSaleDetails' from/to.
export function PeriodPicker({
  value,
  onChange,
}: {
  value: ReportPeriod
  onChange: (period: ReportPeriod) => void
}) {
  const t = useTranslations("dashboard.period")

  return (
    <div className="flex flex-wrap gap-2" role="group" aria-label={t("label")}>
      {PERIODS.map((period) => (
        <Button
          key={period}
          type="button"
          size="sm"
          variant={value === period ? "default" : "outline"}
          aria-pressed={value === period}
          onClick={() => onChange(period)}
        >
          {t(period)}
        </Button>
      ))}
    </div>
  )
}
