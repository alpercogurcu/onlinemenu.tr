"use client"

import axios from "axios"
import { AlertTriangle, Check, Copy, Loader2, Printer, QrCode, RefreshCw, Trash2 } from "lucide-react"
import { useTranslations } from "next-intl"
import { QRCodeSVG } from "qrcode.react"
import type { ComponentProps } from "react"
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
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useCan } from "@/hooks/use-can"
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

  // Cosmetic-only gate (see lib/permissions.ts): a cashier holds
  // storefront.qr.read (can open this dialog and see the active code) but
  // not storefront.qr.manage, so create/rotate/revoke render disabled with a
  // tooltip instead of a working button that would just 403. The backend
  // (authz.rego) is the real enforcement point regardless of this value.
  const canManage = useCan("storefront.qr.manage")

  // The raw token lives here and nowhere else: not in the query cache, not in
  // localStorage, not in a ref that outlives the dialog. The server stores only
  // its SHA-256 hash, so once this component unmounts the value is gone for
  // good and the sticker can only be replaced, never re-printed. Same in-memory
  // rule the CTX token follows in lib/api.ts.
  const [rawToken, setRawToken] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

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
    setCopied(false)
    resetCreate()
    resetRotate()
    resetRevoke()
  }, [open, resetCreate, resetRotate, resetRevoke])

  // The URL is shown as selectable text next to the QR image so the code can
  // also be delivered without a printer (WhatsApp, a note on the table). It is
  // derived from the same in-memory rawToken and dies with it — nothing new is
  // persisted, and this block only ever renders in the moment right after the
  // token was issued.
  async function handleCopy(url: string) {
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      toast.success(t("qr.urlCopied"))
    } catch {
      // Clipboard access is denied on insecure origins and in some embedded
      // browsers; the URL itself is still on screen and selectable, so this is
      // a degraded path, not a failure of the flow.
      toast.error(t("qr.urlCopyFailed"))
    }
  }

  const busy = createQR.isPending || revokeQR.isPending || rotateQR.isPending

  // The disabled+tooltip gate above covers the normal path; this covers the
  // gap it cannot (role changed mid-session, stale client state, etc.) by
  // turning a raw 403 into the same message the tooltip already shows,
  // instead of the generic create/rotate/revoke-failed toast.
  function reportFailure(err: unknown, fallbackKey: "qr.createFailed" | "qr.rotateFailed" | "qr.revokeFailed") {
    if (axios.isAxiosError(err) && err.response?.status === 403) {
      toast.error(t("qr.manageDenied"))
      return
    }
    toast.error(t(fallbackKey))
  }

  async function handleCreate() {
    try {
      const issued = await createQR.mutateAsync({
        branch_id: branchId,
        table_id: tableId,
        table_label: tableLabel,
      })
      setRawToken(issued.token)
      toast.success(t("qr.createdToast"))
    } catch (err) {
      reportFailure(err, "qr.createFailed")
    }
  }

  async function handleRotate() {
    if (!activeCode) return
    try {
      const issued = await rotateQR.mutateAsync(activeCode.id)
      setRawToken(issued.token)
      toast.success(t("qr.rotatedToast"))
    } catch (err) {
      reportFailure(err, "qr.rotateFailed")
    }
  }

  async function handleRevoke() {
    if (!activeCode) return
    try {
      await revokeQR.mutateAsync(activeCode.id)
      setRawToken(null)
      toast.success(t("qr.revokedToast"))
    } catch (err) {
      reportFailure(err, "qr.revokeFailed")
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

            <div className="w-full space-y-1">
              <span className="text-xs font-medium text-muted-foreground">{t("qr.urlLabel")}</span>
              <div className="flex items-center gap-2">
                <code className="min-w-0 flex-1 truncate rounded-md border bg-muted px-2 py-1.5 text-xs select-all">
                  {menuURLFor(rawToken)}
                </code>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  aria-label={t("qr.copyUrl")}
                  onClick={() => void handleCopy(menuURLFor(rawToken))}
                >
                  {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
                </Button>
              </div>
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
                {/* Only "İptal et" is destructive: it retires the printed
                    sticker and leaves the table with no working code. "Yenile"
                    also invalidates the old token, but it hands back a working
                    replacement in the same step, so it must not wear the same
                    red as the one-way action next to it. */}
                <ManageActionButton
                  variant="destructive"
                  onClick={handleRevoke}
                  disabled={busy}
                  canManage={canManage}
                  deniedLabel={t("qr.manageDenied")}
                >
                  {revokeQR.isPending ? (
                    <Loader2 className="size-4 animate-spin" />
                  ) : (
                    <Trash2 className="size-4" />
                  )}
                  {t("qr.revoke")}
                </ManageActionButton>
                <ManageActionButton
                  variant="secondary"
                  onClick={handleRotate}
                  disabled={busy}
                  canManage={canManage}
                  deniedLabel={t("qr.manageDenied")}
                >
                  {rotateQR.isPending ? (
                    <Loader2 className="size-4 animate-spin" />
                  ) : (
                    <RefreshCw className="size-4" />
                  )}
                  {t("qr.rotate")}
                </ManageActionButton>
              </>
            ) : (
              <ManageActionButton
                onClick={handleCreate}
                disabled={busy || branchId === ""}
                canManage={canManage}
                deniedLabel={t("qr.manageDenied")}
              >
                {createQR.isPending ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : (
                  <QrCode className="size-4" />
                )}
                {t("qr.create")}
              </ManageActionButton>
            )}
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// Wraps a manage-only action button (create/rotate/revoke) with the
// disabled+tooltip cosmetic gate: when the current role cannot perform
// storefront.qr.manage (see lib/permissions.ts), the button renders disabled
// and a tooltip explains why, instead of firing a request the backend would
// 403 on. A plain `disabled` Button does not reliably dispatch hover/focus
// events for the tooltip trigger in every browser, hence the focusable
// wrapping span (the standard Radix pattern for disabled-trigger tooltips).
function ManageActionButton({
  canManage,
  deniedLabel,
  disabled,
  children,
  ...buttonProps
}: ComponentProps<typeof Button> & { canManage: boolean; deniedLabel: string }) {
  if (canManage) {
    return (
      <Button disabled={disabled} {...buttonProps}>
        {children}
      </Button>
    )
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span tabIndex={0} className="inline-flex">
          <Button disabled className="pointer-events-none" {...buttonProps}>
            {children}
          </Button>
        </span>
      </TooltipTrigger>
      <TooltipContent>{deniedLabel}</TooltipContent>
    </Tooltip>
  )
}
