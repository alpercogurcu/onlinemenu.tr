"use client"

import { useTranslations } from "next-intl"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectItem } from "@/components/ui/select"
import type { SelectionType } from "@/types"

const MAX_SELECTION_OPTIONS = [2, 3, 4, 5, 6, 7, 8, 9, 10]

interface ModifierGroupFormProps {
  name: string
  onNameChange: (value: string) => void
  selectionType: SelectionType
  onSelectionTypeChange: (value: SelectionType) => void
  isRequired: boolean
  onIsRequiredChange: (value: boolean) => void
  // Only meaningful (and only rendered) while selectionType is "multiple" —
  // a single-selection group is always capped at 1, see modifier-group-editor.
  maxSelections: number | null
  onMaxSelectionsChange: (value: number | null) => void
  nameError?: string
  maxError?: string
}

// The "Kural" card: the three facts that define how a group behaves for the
// customer (selection_type, is_required, max_selections). No <select>/radio
// native primitive matches the segmented-button look the design calls for, so
// each segmented control is a pair of plain toggle Buttons sharing a
// role="group" — aria-pressed carries the selection state to assistive tech.
export function ModifierGroupForm({
  name,
  onNameChange,
  selectionType,
  onSelectionTypeChange,
  isRequired,
  onIsRequiredChange,
  maxSelections,
  onMaxSelectionsChange,
  nameError,
  maxError,
}: ModifierGroupFormProps) {
  const t = useTranslations("catalog.group")

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("sections.rule")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-5">
        <div className="space-y-2">
          <Label htmlFor="group-name">
            {t("fields.name")}
            {"*"}
          </Label>
          <Input
            id="group-name"
            value={name}
            onChange={(e) => onNameChange(e.target.value)}
            aria-invalid={Boolean(nameError)}
          />
          <p className="text-xs text-muted-foreground">{t("fields.nameHint")}</p>
          {nameError ? <p className="text-sm text-destructive">{nameError}</p> : null}
        </div>

        <div className="space-y-2">
          <span className="text-sm font-medium">{t("fields.selection")}</span>
          <div className="flex gap-2" role="group" aria-label={t("fields.selection")}>
            <Button
              type="button"
              variant={selectionType === "single" ? "default" : "outline"}
              aria-pressed={selectionType === "single"}
              onClick={() => onSelectionTypeChange("single")}
            >
              {t("fields.selectionSingle")}
            </Button>
            <Button
              type="button"
              variant={selectionType === "multiple" ? "default" : "outline"}
              aria-pressed={selectionType === "multiple"}
              onClick={() => onSelectionTypeChange("multiple")}
            >
              {t("fields.selectionMultiple")}
            </Button>
          </div>
        </div>

        <div className="space-y-2">
          <span className="text-sm font-medium">{t("fields.required")}</span>
          <div className="flex gap-2" role="group" aria-label={t("fields.required")}>
            <Button
              type="button"
              variant={!isRequired ? "default" : "outline"}
              aria-pressed={!isRequired}
              onClick={() => onIsRequiredChange(false)}
            >
              {t("fields.requiredNo")}
            </Button>
            <Button
              type="button"
              variant={isRequired ? "default" : "outline"}
              aria-pressed={isRequired}
              onClick={() => onIsRequiredChange(true)}
            >
              {t("fields.requiredYes")}
            </Button>
          </div>
        </div>

        {selectionType === "multiple" ? (
          <div className="space-y-2">
            <Label htmlFor="group-max">{t("fields.max")}</Label>
            <Select
              id="group-max"
              value={maxSelections === null ? "unlimited" : String(maxSelections)}
              onValueChange={(value) =>
                onMaxSelectionsChange(value === "unlimited" ? null : Number(value))
              }
              aria-invalid={Boolean(maxError)}
            >
              <SelectItem value="unlimited">{t("fields.maxUnlimited")}</SelectItem>
              {MAX_SELECTION_OPTIONS.map((n) => (
                <SelectItem key={n} value={String(n)}>
                  {n}
                </SelectItem>
              ))}
            </Select>
            <p className="text-xs text-muted-foreground">{t("fields.maxHint")}</p>
            {maxError ? <p className="text-sm text-destructive">{maxError}</p> : null}
          </div>
        ) : null}
      </CardContent>
    </Card>
  )
}
