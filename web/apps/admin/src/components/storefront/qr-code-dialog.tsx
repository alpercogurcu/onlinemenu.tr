"use client"

import { AlertTriangle, Loader2, Printer, QrCode, RefreshCw, Trash2 } from "lucide-react"
import { useTranslations } from "next-intl"
import { QRCodeSVG } from "qrcode.react"
import { useEffect, useState } from "react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import {
  useCreateQRCode,
  useQRCodes,
  useRevokeQRCode,
  useRotateQRCode,
} from "@/hooks/use-storefront"
import type { QRCode } from "@/types"

// Where the printed sticker points. The guest app (web/apps/menu) resolves
// /q/{token} by POSTing the token in the request BODY — the token never rides
// in an API path (ADR-ARCH-006 D1), but it must be in the printed URL, because
// the sticker is the only place it exists after this dialog closes.
const MENU_BASE_URL = process.env.NEXT_PUBLIC_MENU_URL ?? "http://localhost:3001"

function menuURLFor(token: string): string {
  return `${MENU_BASE_URL.replace(/\/$/, "")}/q/${token}`
}

interface QRCodeDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  branchId: string
  tableId: string
  tableLabel: string
}

export function QRCodeDialog({
  open,
  onOpenChange,
  branchId,
  tableId,
  tableLabel,
}: QRCodeDialogProps) {
  const t = useTranslations("storefront")

  // The raw token lives here and nowhere else: not in the query cache, not in
  // localStorage, not in a ref that outlives the dialog. The server stores only
  // its SHA-256 hash, so once this component unmounts the value is gone for
  // good and the sticker can only be replaced, never re-printed. Same in-memory
  // rule the CTX token follows in lib/api.ts.
  const [rawToken, setRawToken] = useState<string | null>(null)

  const { data: codes, isLoading } = useQRCodes(open ? branchId : "")
  const createQR = useCreateQRCode()
  const revokeQR = useRevokeQRCode()
  const rotateQR = useRotateQRCode()

  const activeCode: QRCode | undefined = codes?.find(
    (c) => c.table_id === tableId && c.status === "active",
  )

  // Clearing on close is what makes the "in memory only" claim true even if a
  // caller keeps this component mounted across open/close cycles: without it,
  // the previous session's token would still be held (and re-rendered) the next
  // time the dialog opens. pos/tables/page.tsx additionally unmounts and re-keys
  // the dialog per table, so in practice this is the second of two guarantees.
  const resetCreate = createQR.reset
  const resetRotate = rotateQR.reset
  const resetRevoke = revokeQR.reset
  useEffect(() => {
    if (open) return
    setRawToken(null)
    resetCreate()
    resetRotate()
    resetRevoke()
  }, [open, resetCreate, resetRotate, resetRevoke])

  const busy = createQR.isPending || revokeQR.isPending || rotateQR.isPending

  async function handleCreate() {
    try {
      const issued = await createQR.mutateAsync({
        branch_id: branchId,
        table_id: tableId,
        table_label: tableLabel,
      })
      setRawToken(issued.token)
      toast.success(t("qr.createdToast"))
    } catch {
      toast.error(t("qr.createFailed"))
    }
  }

  async function handleRotate() {
    if (!activeCode) return
    try {
      const issued = await rotateQR.mutateAsync(activeCode.id)
      setRawToken(issued.token)
      toast.success(t("qr.rotatedToast"))
    } catch {
      toast.error(t("qr.rotateFailed"))
    }
  }

  async function handleRevoke() {
    if (!activeCode) return
    try {
      await revokeQR.mutateAsync(activeCode.id)
      setRawToken(null)
      toast.success(t("qr.revokedToast"))
    } catch {
      toast.error(t("qr.revokeFailed"))
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <QrCode className="size-4" />
            {t("qr.title", { table: tableLabel })}
          </DialogTitle>
          <DialogDescription>{t("qr.description")}</DialogDescription>
        </DialogHeader>

        {isLoading ? (
          <Skeleton className="h-56 w-full rounded-lg" />
        ) : rawToken ? (
          <div className="flex flex-col items-center gap-3">
            <div
              id="qr-print-area"
              className="flex flex-col items-center gap-3 rounded-lg border bg-white p-6"
            >
              <QRCodeSVG value={menuURLFor(rawToken)} size={220} level="M" marginSize={2} />
              <span className="text-sm font-semibold text-black">{tableLabel}</span>
            </div>
            <p className="flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800">
              <AlertTriangle className="mt-0.5 size-4 shrink-0" />
              {t("qr.oneTimeWarning")}
            </p>
          </div>
        ) : activeCode ? (
          <div className="flex flex-col items-center gap-3 py-4">
            <Badge variant="outline" className="border-green-200 bg-green-100 text-green-700">
              {t("qr.statusActive")}
            </Badge>
            <p className="text-center text-sm text-muted-foreground">
              {t("qr.tokenNotRecoverable")}
            </p>
          </div>
        ) : (
          <div className="flex flex-col items-center gap-3 py-6">
            <QrCode className="size-10 text-muted-foreground" />
            <p className="text-center text-sm text-muted-foreground">{t("qr.noActiveCode")}</p>
          </div>
        )}

        <DialogFooter className="gap-2 sm:justify-between">
          {rawToken && (
            <Button variant="outline" onClick={() => window.print()}>
              <Printer className="size-4" />
              {t("qr.print")}
            </Button>
          )}

          <div className="flex gap-2">
            {activeCode ? (
              <>
                <Button variant="destructive" onClick={handleRevoke} disabled={busy}>
                  {revokeQR.isPending ? (
                    <Loader2 className="size-4 animate-spin" />
                  ) : (
                    <Trash2 className="size-4" />
                  )}
                  {t("qr.revoke")}
                </Button>
                <Button onClick={handleRotate} disabled={busy}>
                  {rotateQR.isPending ? (
                    <Loader2 className="size-4 animate-spin" />
                  ) : (
                    <RefreshCw className="size-4" />
                  )}
                  {t("qr.rotate")}
                </Button>
              </>
            ) : (
              <Button onClick={handleCreate} disabled={busy || branchId === ""}>
                {createQR.isPending ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : (
                  <QrCode className="size-4" />
                )}
                {t("qr.create")}
              </Button>
            )}
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
