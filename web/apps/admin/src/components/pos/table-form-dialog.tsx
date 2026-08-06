"use client"

import { Loader2 } from "lucide-react"
import { useTranslations } from "next-intl"
import { useState } from "react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectItem } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { useCreateTable, useUpdateTable } from "@/hooks/use-pos"
import type { PosTable, PosZone } from "@/types"

interface TableFormDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  branchId: string
  // Zones come from GET /zones rather than from the floor plan: a zone with no
  // table yet is missing from the plan, and that is precisely the zone the
  // operator wants to fill right after creating it.
  zones: PosZone[]
  // Absent -> create mode; present -> edit mode (PATCH /tables/{id}).
  table?: PosTable
  defaultZoneId?: string
}

export function TableFormDialog({
  open,
  onOpenChange,
  branchId,
  zones,
  table,
  defaultZoneId,
}: TableFormDialogProps) {
  const t = useTranslations("posTables.tableForm")
  const isEdit = table !== undefined

  const [zoneId, setZoneId] = useState(table?.zone_id ?? defaultZoneId ?? zones[0]?.id ?? "")
  const [name, setName] = useState(table?.name ?? "")
  const [capacity, setCapacity] = useState(String(table?.capacity ?? 4))
  const [isActive, setIsActive] = useState(table?.is_active ?? true)

  const createTable = useCreateTable()
  const updateTable = useUpdateTable()
  const pending = createTable.isPending || updateTable.isPending

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const trimmed = name.trim()
    if (zoneId === "") {
      toast.error(t("zoneRequired"))
      return
    }
    if (trimmed === "") {
      toast.error(t("nameRequired"))
      return
    }
    const parsedCapacity = Number(capacity)
    if (!Number.isInteger(parsedCapacity) || parsedCapacity < 1) {
      toast.error(t("capacityInvalid"))
      return
    }

    try {
      if (isEdit) {
        await updateTable.mutateAsync({
          id: table.id,
          zone_id: zoneId,
          name: trimmed,
          capacity: parsedCapacity,
          is_active: isActive,
        })
        toast.success(t("updated"))
      } else {
        await createTable.mutateAsync({
          branch_id: branchId,
          zone_id: zoneId,
          name: trimmed,
          capacity: parsedCapacity,
        })
        toast.success(t("created"))
      }
      onOpenChange(false)
    } catch {
      toast.error(isEdit ? t("updateFailed") : t("createFailed"))
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{isEdit ? t("editTitle") : t("createTitle")}</DialogTitle>
          <DialogDescription>{t("description")}</DialogDescription>
        </DialogHeader>

        {zones.length === 0 ? (
          <p className="py-4 text-sm text-muted-foreground">{t("noZones")}</p>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="table-zone">{t("zone")}</Label>
              <Select id="table-zone" value={zoneId} onValueChange={setZoneId}>
                {/* Passive zones stay selectable (the backend accepts them and
                    an operator may be preparing a section before opening it)
                    but are labelled, so a table does not silently land in a
                    zone that is switched off. */}
                {zones.map((zone) => (
                  <SelectItem key={zone.id} value={zone.id}>
                    {zone.is_active ? zone.name : t("zonePassiveOption", { zone: zone.name })}
                  </SelectItem>
                ))}
              </Select>
            </div>

            <div className="space-y-2">
              <Label htmlFor="table-name">{t("name")}</Label>
              <Input
                id="table-name"
                value={name}
                placeholder={t("namePlaceholder")}
                onChange={(e) => setName(e.target.value)}
                autoFocus
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="table-capacity">{t("capacity")}</Label>
              <Input
                id="table-capacity"
                type="number"
                min={1}
                value={capacity}
                onChange={(e) => setCapacity(e.target.value)}
              />
            </div>

            {isEdit && (
              <div className="flex items-center gap-3">
                <Switch id="table-active" checked={isActive} onCheckedChange={setIsActive} />
                <Label htmlFor="table-active">{t("active")}</Label>
              </div>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                {t("cancel")}
              </Button>
              <Button type="submit" disabled={pending || branchId === ""}>
                {pending && <Loader2 className="size-4 animate-spin" />}
                {t("save")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
