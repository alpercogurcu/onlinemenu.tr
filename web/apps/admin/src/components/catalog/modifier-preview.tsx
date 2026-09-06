"use client"

import { useTranslations } from "next-intl"

import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { formatKurus } from "@/lib/money"
import type { Modifier, SelectionType } from "@/types"

interface ModifierPreviewProps {
  name: string
  selectionType: SelectionType
  isRequired: boolean
  modifiers: Modifier[]
}

// "+₺60,00" for a surcharge, "-₺20,00" for a discount (allowNegative price
// deltas), "Ücretsiz" for exactly zero — formatKurus already renders the
// magnitude with the app's canonical TRY formatting, this only adds the sign.
function formatDelta(delta: number, freeLabel: string): string {
  if (delta === 0) return freeLabel
  const amount = formatKurus(Math.abs(delta))
  return delta > 0 ? `+${amount}` : `-${amount}`
}

// Read-only mockup of what the customer-facing order screen renders for this
// group: only options currently on sale (is_active) appear, ordered the same
// way the "Seçenekler" card lists them. Inputs are disabled — this card never
// mutates anything, it only reflects the live form state above it.
export function ModifierPreview({ name, selectionType, isRequired, modifiers }: ModifierPreviewProps) {
  const t = useTranslations("catalog.group.preview")

  const active = [...modifiers].filter((m) => m.is_active).sort((a, b) => a.sort_order - b.sort_order)

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center justify-between gap-2">
          <span>{name || t("title")}</span>
          <Badge variant="outline">{isRequired ? t("required") : t("optional")}</Badge>
        </CardTitle>
        <CardDescription>{t("hint")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {active.map((modifier) => (
          <label key={modifier.id} className="flex items-center justify-between gap-2 text-sm">
            <span className="flex items-center gap-2">
              {selectionType === "single" ? (
                <input type="radio" name="modifier-preview" disabled className="size-4" />
              ) : (
                <input type="checkbox" disabled className="size-4" />
              )}
              {modifier.name}
            </span>
            <span className="text-muted-foreground tabular-nums">
              {formatDelta(modifier.price_delta, t("free"))}
            </span>
          </label>
        ))}
      </CardContent>
    </Card>
  )
}
