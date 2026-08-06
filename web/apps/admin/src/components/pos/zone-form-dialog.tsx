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
import { Switch } from "@/components/ui/switch"
import { useCreateZone, useUpdateZone } from "@/hooks/use-pos"
import type { PosZone } from "@/types"

interface ZoneFormDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  branchId: string
  // Absent -> create mode; present -> edit mode (PATCH /zones/{id}).
  zone?: PosZone
}

export function ZoneFormDialog({ open, onOpenChange, branchId, zone }: ZoneFormDialogProps) {
  const t = useTranslations("posTables.zoneForm")
  const isEdit = zone !== undefined

  // Floor is held as text, not a number: an <input type="number"> is empty
  // while the user clears it to retype, and coercing that to 0 mid-edit would
  // silently move the zone to the ground floor.
  const [name, setName] = useState(zone?.name ?? "")
  const [floor, setFloor] = useState(String(zone?.floor ?? 0))
  const [isActive, setIsActive] = useState(zone?.is_active ?? true)

  const createZone = useCreateZone()
  const updateZone = useUpdateZone()
  const pending = createZone.isPending || updateZone.isPending

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const trimmed = name.trim()
    if (trimmed === "") {
      toast.error(t("nameRequired"))
      return
    }
    const parsedFloor = Number(floor)
    if (!Number.isInteger(parsedFloor)) {
      toast.error(t("floorInvalid"))
      return
    }

    try {
      if (isEdit) {
        await updateZone.mutateAsync({
          id: zone.id,
          name: trimmed,
          floor: parsedFloor,
          is_active: isActive,
        })
        toast.success(t("updated"))
      } else {
        await createZone.mutateAsync({ branch_id: branchId, name: trimmed, floor: parsedFloor })
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

        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="zone-name">{t("name")}</Label>
            <Input
              id="zone-name"
              value={name}
              placeholder={t("namePlaceholder")}
              onChange={(e) => setName(e.target.value)}
              autoFocus
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="zone-floor">{t("floor")}</Label>
            <Input
              id="zone-floor"
              type="number"
              value={floor}
              onChange={(e) => setFloor(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">{t("floorHint")}</p>
          </div>

          {isEdit && (
            <div className="flex items-center gap-3">
              <Switch id="zone-active" checked={isActive} onCheckedChange={setIsActive} />
              <Label htmlFor="zone-active">{t("active")}</Label>
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
      </DialogContent>
    </Dialog>
  )
}
