"use client"

import { TriangleAlert } from "lucide-react"
import { useTranslations } from "next-intl"
import { useState } from "react"
import { toast } from "sonner"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Switch } from "@/components/ui/switch"
import { useCan } from "@/hooks/use-can"
import {
  usePosBranchSettings,
  useUpdatePosBranchSettings,
  type PosBranchSettingsInput,
} from "@/hooks/use-pos-branch-settings"
import { useSelectedBranch } from "@/hooks/use-selected-branch"
import { formatKurusForInput, parseLiraToKurus } from "@/lib/money"
import type { PosOrderFlow, RoundingStepMinor, WaiterCategoryLayout } from "@/types"

const LAYOUTS: WaiterCategoryLayout[] = ["top", "side"]
const FLOWS: PosOrderFlow[] = ["full", "simple"]
const ROUNDING_STEPS: RoundingStepMinor[] = [50, 100, 500, 1000]
// Backend default (pos/000014) and its accepted ceiling range.
const DEFAULT_ROUNDING_STEP: RoundingStepMinor = 500
const DEFAULT_ROUNDING_MAX = 1000
const MAX_ROUNDING_CEILING = 10000

function roundingStepFrom(value: string): RoundingStepMinor | null {
  const n = Number(value)
  return ROUNDING_STEPS.find((s) => s === n) ?? null
}

// Per-branch POS preferences on the Şubeler page. The card edits the GLOBAL
// branch (header switcher) and names it in its title — the per-card branch
// Select is gone. Gated on pos.table.manage the same way the QR toggle on
// Masalar is: a role without the permission gets neither the controls nor
// the GET (the section simply does not exist).
export function PosSettingsCard() {
  const t = useTranslations("branchPosSettings")
  const canManage = useCan("pos.table.manage")
  const { branchId, branch } = useSelectedBranch()

  const { data: settings, isLoading } = usePosBranchSettings(branchId, { enabled: canManage })
  const update = useUpdatePosBranchSettings()

  if (!canManage) return null

  const busy = isLoading || update.isPending
  const layout = settings?.waiter_category_layout ?? "top"
  const flow = settings?.order_flow ?? "full"
  const roundingCash = settings?.rounding_cash_enabled ?? false
  const roundingCard = settings?.rounding_card_enabled ?? false
  const roundingStep = settings?.rounding_step_minor ?? DEFAULT_ROUNDING_STEP
  const roundingMax = settings?.rounding_max_per_check_minor ?? DEFAULT_ROUNDING_MAX

  // The radios are controlled by the query cache, so a failed PUT needs no
  // manual rollback: the cache never changed, the selection stays where it was.
  async function save(patch: Omit<PosBranchSettingsInput, "branch_id">) {
    try {
      await update.mutateAsync({ branch_id: branchId, ...patch })
      toast.success(t("saved"))
    } catch {
      toast.error(t("saveFailed"))
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          {t("title")}
          {branch ? ` — ${branch.name}` : ""}
        </CardTitle>
        <CardDescription>{t("subtitle")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-6">
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
          <fieldset className="space-y-3">
            <legend className="text-sm font-medium">{t("layoutTitle")}</legend>
            <RadioGroup
              name="waiter-category-layout"
              value={layout}
              onValueChange={(value) => {
                if (value !== layout) void save({ waiter_category_layout: value === "side" ? "side" : "top" })
              }}
            >
              {LAYOUTS.map((value) => (
                <div key={value} className="flex items-start gap-2">
                  <RadioGroupItem
                    id={`layout-${value}`}
                    value={value}
                    className="mt-1"
                    disabled={busy || branchId === ""}
                  />
                  <Label htmlFor={`layout-${value}`} className="font-normal leading-5">
                    {t(value === "top" ? "layoutTop" : "layoutSide")}
                  </Label>
                </div>
              ))}
            </RadioGroup>
            <p className="text-muted-foreground text-xs">{t("layoutHint")}</p>
          </fieldset>

          <fieldset className="space-y-3">
            <legend className="text-sm font-medium">{t("flowTitle")}</legend>
            <RadioGroup
              name="order-flow"
              value={flow}
              onValueChange={(value) => {
                if (value !== flow) void save({ order_flow: value === "simple" ? "simple" : "full" })
              }}
            >
              {FLOWS.map((value) => (
                <div key={value} className="flex items-start gap-2">
                  <RadioGroupItem
                    id={`flow-${value}`}
                    value={value}
                    className="mt-1"
                    disabled={busy || branchId === ""}
                  />
                  <Label htmlFor={`flow-${value}`} className="font-normal leading-5">
                    {t(value === "full" ? "flowFull" : "flowSimple")}
                  </Label>
                </div>
              ))}
            </RadioGroup>
            <p className="text-muted-foreground text-xs">{t("flowHint")}</p>
            <div className="flex items-start gap-2 rounded-md border border-status-warning-border bg-status-warning-bg px-3 py-2 text-xs text-status-warning-fg">
              <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
              <span>{t("flowWarning")}</span>
            </div>
          </fieldset>
        </div>

        <fieldset className="space-y-4 border-t pt-6">
          <legend className="text-sm font-medium">{t("roundingTitle")}</legend>
          <p className="text-muted-foreground text-xs">{t("roundingHint")}</p>
          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <div className="space-y-3">
              <div className="flex items-center justify-between gap-3">
                <Label htmlFor="rounding-cash" className="font-normal leading-5">
                  {t("roundingCash")}
                </Label>
                <Switch
                  id="rounding-cash"
                  checked={roundingCash}
                  disabled={busy || branchId === ""}
                  onCheckedChange={(checked) => void save({ rounding_cash_enabled: checked })}
                />
              </div>
              <div className="flex items-center justify-between gap-3">
                <Label htmlFor="rounding-card" className="font-normal leading-5">
                  {t("roundingCard")}
                </Label>
                <Switch
                  id="rounding-card"
                  checked={roundingCard}
                  disabled={busy || branchId === ""}
                  onCheckedChange={(checked) => void save({ rounding_card_enabled: checked })}
                />
              </div>
            </div>

            <div className="space-y-4">
              <div className="space-y-2">
                <p className="text-sm">{t("roundingStepTitle")}</p>
                <RadioGroup
                  name="rounding-step"
                  value={String(roundingStep)}
                  className="flex flex-wrap gap-4"
                  onValueChange={(value) => {
                    const step = roundingStepFrom(value)
                    if (step !== null && step !== roundingStep) void save({ rounding_step_minor: step })
                  }}
                >
                  {ROUNDING_STEPS.map((step) => (
                    <div key={step} className="flex items-center gap-2">
                      <RadioGroupItem
                        id={`rounding-step-${step}`}
                        value={String(step)}
                        disabled={busy || branchId === ""}
                      />
                      <Label htmlFor={`rounding-step-${step}`} className="font-normal">
                        {t(`roundingStep${step}`)}
                      </Label>
                    </div>
                  ))}
                </RadioGroup>
              </div>
              <RoundingMaxField
                // Remount on a server-side change so the field never shows a
                // stale draft for another branch or after a save.
                key={`${branchId}:${roundingMax}`}
                value={roundingMax}
                disabled={busy || branchId === ""}
                onSave={(kurus) => void save({ rounding_max_per_check_minor: kurus })}
              />
            </div>
          </div>
          <div className="flex items-start gap-2 rounded-md border border-status-warning-border bg-status-warning-bg px-3 py-2 text-xs text-status-warning-fg">
            <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
            <span>{t("roundingDeviceWarning")}</span>
          </div>
        </fieldset>
      </CardContent>
    </Card>
  )
}

// The per-check ceiling is typed in lira and saved on blur/Enter, not per
// keystroke: a half-typed "1" must not become a ₺1 ceiling on the server.
function RoundingMaxField({
  value,
  disabled,
  onSave,
}: {
  value: number
  disabled: boolean
  onSave: (kurus: number) => void
}) {
  const t = useTranslations("branchPosSettings")
  const [draft, setDraft] = useState(formatKurusForInput(value))
  const parsed = parseLiraToKurus(draft)
  const invalid = parsed === null || parsed > MAX_ROUNDING_CEILING

  function commit() {
    if (invalid || parsed === value) return
    onSave(parsed)
  }

  return (
    <div className="space-y-1.5">
      <Label htmlFor="rounding-max" className="text-sm font-normal">
        {t("roundingMaxTitle")}
      </Label>
      <Input
        id="rounding-max"
        inputMode="decimal"
        className="max-w-40 tabular-nums"
        value={draft}
        disabled={disabled}
        aria-invalid={invalid}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === "Enter") commit()
        }}
      />
      <p className={invalid ? "text-destructive text-xs" : "text-muted-foreground text-xs"}>
        {invalid ? t("roundingMaxInvalid") : t("roundingMaxHint")}
      </p>
    </div>
  )
}
