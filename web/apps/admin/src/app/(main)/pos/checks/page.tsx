"use client"

import { ClipboardList, QrCode, Users } from "lucide-react"
import { useTranslations } from "next-intl"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useCancelCheck, useChecks, useCloseCheck } from "@/hooks/use-pos"
import { checkStatusVariant } from "@/lib/status-badge"
import { cn } from "@/lib/utils"
import {
  formatCheckDuration,
  formatCheckTotal,
  formatOpenDuration,
  isLongOpenCheck,
} from "@/lib/pos-format"
import type { Check } from "@/types"
import { toast } from "sonner"

// durationFor renders the "Süre" column with two different formatters on
// purpose. An open check is a live, still-growing figure and keeps the
// relative-time reading ("az önce", "3s+" once it has been open too long).
// A closed/cancelled one is a finished span measured opened_at -> closed_at,
// so it must be a duration: a QR check that lived 14 seconds reads "14 sn",
// where formatOpenDuration would have printed "az önce" — forever, including
// a month later.
function durationFor(check: Check): string {
  if (check.status === "open") return formatOpenDuration(check.opened_at)
  if (!check.closed_at) return "—"
  return formatCheckDuration(check.opened_at, new Date(check.closed_at))
}

export default function ChecksPage() {
  const t = useTranslations("posChecks")
  const { data, isLoading } = useChecks({ refetchInterval: 30_000 })
  const closeCheck = useCloseCheck()
  const cancelCheck = useCancelCheck()

  const checks = data ?? []

  const handleClose = async (id: string, label: string) => {
    try {
      await closeCheck.mutateAsync(id)
      toast.success(t("closed", { table: label }))
    } catch {
      toast.error(t("closeFailed"))
    }
  }

  const handleCancel = async (id: string, label: string) => {
    try {
      await cancelCheck.mutateAsync(id)
      toast.success(t("cancelled", { table: label }))
    } catch {
      toast.error(t("cancelFailed"))
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">{t("title")}</h1>
        <p className="text-muted-foreground">{t("subtitle")}</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t("listTitle")}</CardTitle>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-12 w-full" />
              ))}
            </div>
          ) : checks.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <ClipboardList className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">{t("empty")}</h3>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("columnTable")}</TableHead>
                  <TableHead className="text-center">{t("columnPax")}</TableHead>
                  <TableHead className="text-right">{t("columnTotal")}</TableHead>
                  <TableHead>{t("columnNote")}</TableHead>
                  <TableHead>{t("columnStatus")}</TableHead>
                  <TableHead>{t("columnOpenedAt")}</TableHead>
                  <TableHead>{t("columnClosedAt")}</TableHead>
                  <TableHead>{t("columnDuration")}</TableHead>
                  <TableHead className="w-[160px]">{t("columnActions")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {checks.map((check) => (
                  <TableRow key={check.id}>
                    <TableCell className="font-medium">
                      <span className="flex items-center gap-2">
                        {check.table_label}
                        {/* Rendered only when the API actually sends `source`.
                            It does not today (checkResponse omits the field —
                            see the CheckSource doc comment in types), so this
                            badge stays invisible until the backend exposes it,
                            rather than mislabelling every check as POS. */}
                        {check.source === "online_qr" && (
                          <Badge variant="info">
                            <QrCode className="size-3" />
                            {t("sourceOnlineQr")}
                          </Badge>
                        )}
                      </span>
                    </TableCell>
                    <TableCell className="text-center tabular-nums">
                      <span className="inline-flex items-center gap-1">
                        <Users className="size-3.5 text-muted-foreground" />
                        {check.pax}
                      </span>
                    </TableCell>
                    <TableCell className="text-right font-medium tabular-nums">
                      {formatCheckTotal(check.total)}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-xs">{check.note || "—"}</TableCell>
                    <TableCell>
                      <Badge variant={checkStatusVariant(check.status)}>
                        {t(`status.${check.status}`)}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {new Date(check.opened_at).toLocaleString("tr-TR")}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {check.closed_at
                        ? new Date(check.closed_at).toLocaleString("tr-TR")
                        : "—"}
                    </TableCell>
                    <TableCell
                      className={cn(
                        "text-sm tabular-nums",
                        check.status === "open" && isLongOpenCheck(check.opened_at)
                          ? "font-medium text-status-warning-fg"
                          : "text-muted-foreground",
                      )}
                    >
                      {durationFor(check)}
                    </TableCell>
                    <TableCell>
                      {check.status === "open" && (
                        <div className="flex gap-2">
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => handleClose(check.id, check.table_label)}
                            disabled={closeCheck.isPending}
                          >
                            {t("close")}
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => handleCancel(check.id, check.table_label)}
                            disabled={cancelCheck.isPending}
                            className="text-destructive hover:text-destructive"
                          >
                            {t("cancel")}
                          </Button>
                        </div>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
