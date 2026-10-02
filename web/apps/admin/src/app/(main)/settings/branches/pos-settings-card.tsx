"use client"

import { TriangleAlert } from "lucide-react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Label } from "@/components/ui/label"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { useCan } from "@/hooks/use-can"
import {
  usePosBranchSettings,
  useUpdatePosBranchSettings,
  type PosBranchSettingsInput,
} from "@/hooks/use-pos-branch-settings"
import { useSelectedBranch } from "@/hooks/use-selected-branch"
import type { PosOrderFlow, WaiterCategoryLayout } from "@/types"

const LAYOUTS: WaiterCategoryLayout[] = ["top", "side"]
const FLOWS: PosOrderFlow[] = ["full", "simple"]

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
      </CardContent>
    </Card>
  )
}
