"use client"

import { ChevronRight, ClipboardList, QrCode, Users } from "lucide-react"
import { useTranslations } from "next-intl"

import Link from "next/link"
import { useRouter } from "next/navigation"

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
import { ConfirmDialog } from "@/components/catalog/confirm-dialog"
import { useCan } from "@/hooks/use-can"
import { useCancelCheck, useChecks, useCloseCheck } from "@/hooks/use-pos"
import { checkStatusVariant } from "@/lib/status-badge"
import { cn } from "@/lib/utils"
import { checkDurationLabel, formatCheckTotal, isLongOpenCheck } from "@/lib/pos-format"
import { describeCheckActionError } from "@/lib/pos-order"
import { useState } from "react"
import { toast } from "sonner"
import type { Check } from "@/types"

type Filter = "open" | "all"

export default function ChecksPage() {
  const t = useTranslations("posChecks")
  const router = useRouter()
  // Close/cancel are counter decisions (cashier/shift_manager + manager); a
  // waiter reads checks but gets no buttons that the API would refuse.
  const canClose = useCan("pos.check.close")
  const canCancel = useCan("pos.check.cancel")
  const hasActions = canClose || canCancel
  const { data, isLoading } = useChecks({ refetchInterval: 30_000 })
  const closeCheck = useCloseCheck()
  const cancelCheck = useCancelCheck()

  // The counter works the open checks; the closed/cancelled history is one
  // tap away instead of burying the live ones under yesterday's rows.
  const [filter, setFilter] = useState<Filter>("open")
  const [cancelTarget, setCancelTarget] = useState<Check | null>(null)

  const all = data ?? []
  const openCount = all.filter((c) => c.status === "open").length
  const checks = filter === "open" ? all.filter((c) => c.status === "open") : all

  const handleClose = async (id: string, label: string) => {
    try {
      await closeCheck.mutateAsync(id)
      toast.success(t("closed", { table: label }))
    } catch (err) {
      toast.error(t("closeFailed"), { description: describeCheckActionError(err) })
    }
  }

  const handleCancel = async (check: Check) => {
    try {
      await cancelCheck.mutateAsync(check.id)
      toast.success(t("cancelled", { table: check.table_label }))
    } catch (err) {
      toast.error(t("cancelFailed"), { description: describeCheckActionError(err) })
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">{t("title")}</h1>
        <p className="text-muted-foreground">{t("subtitle")}</p>
      </div>

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 space-y-0">
          <CardTitle>{t("listTitle")}</CardTitle>
          <div className="flex gap-2" role="group" aria-label={t("filterLabel")}>
            <Button
              size="sm"
              variant={filter === "open" ? "default" : "outline"}
              aria-pressed={filter === "open"}
              onClick={() => setFilter("open")}
            >
              {t("filterOpen", { count: openCount })}
            </Button>
            <Button
              size="sm"
              variant={filter === "all" ? "default" : "outline"}
              aria-pressed={filter === "all"}
              onClick={() => setFilter("all")}
            >
              {t("filterAll")}
            </Button>
          </div>
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
              <h3 className="text-lg font-semibold">{filter === "open" ? t("emptyOpen") : t("empty")}</h3>
              {filter === "open" && all.length > 0 && (
                <Button variant="outline" className="mt-4" onClick={() => setFilter("all")}>
                  {t("showAll")}
                </Button>
              )}
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("columnTable")}</TableHead>
                  <TableHead className="hidden text-center sm:table-cell">{t("columnPax")}</TableHead>
                  <TableHead className="text-right">{t("columnTotal")}</TableHead>
                  <TableHead className="hidden md:table-cell">{t("columnNote")}</TableHead>
                  <TableHead>{t("columnStatus")}</TableHead>
                  <TableHead className="hidden md:table-cell">{t("columnOpenedAt")}</TableHead>
                  <TableHead className="hidden md:table-cell">{t("columnClosedAt")}</TableHead>
                  <TableHead className="hidden md:table-cell">{t("columnDuration")}</TableHead>
                  {hasActions && <TableHead className="w-[160px]">{t("columnActions")}</TableHead>}
                  <TableHead className="w-8" aria-hidden />
                </TableRow>
              </TableHeader>
              <TableBody>
                {checks.map((check) => (
                  <TableRow
                    key={check.id}
                    className="cursor-pointer"
                    onClick={() => router.push(`/pos/checks/${check.id}`)}
                  >
                    <TableCell className="font-medium">
                      <span className="flex items-center gap-2">
                        {/* The row is clickable for the pointer; this link is the
                            keyboard/screen-reader path to the same page. */}
                        <Link
                          href={`/pos/checks/${check.id}`}
                          className="hover:underline"
                          aria-label={t("openDetail", { table: check.table_label })}
                          onClick={(e) => e.stopPropagation()}
                        >
                          {check.table_label}
                        </Link>
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
                    <TableCell className="hidden text-center tabular-nums sm:table-cell">
                      <span className="inline-flex items-center gap-1">
                        <Users className="size-3.5 text-muted-foreground" />
                        {check.pax}
                      </span>
                    </TableCell>
                    <TableCell className="text-right text-base font-semibold tabular-nums">
                      {formatCheckTotal(check.total)}
                    </TableCell>
                    <TableCell className="hidden text-muted-foreground text-xs md:table-cell">{check.note || "—"}</TableCell>
                    <TableCell>
                      <Badge variant={checkStatusVariant(check.status)}>
                        {t(`status.${check.status}`)}
                      </Badge>
                    </TableCell>
                    <TableCell className="hidden text-muted-foreground text-sm md:table-cell">
                      {new Date(check.opened_at).toLocaleString("tr-TR")}
                    </TableCell>
                    <TableCell className="hidden text-muted-foreground text-sm md:table-cell">
                      {check.closed_at
                        ? new Date(check.closed_at).toLocaleString("tr-TR")
                        : "—"}
                    </TableCell>
                    <TableCell
                      className={cn(
                        "hidden text-sm tabular-nums md:table-cell",
                        check.status === "open" && isLongOpenCheck(check.opened_at)
                          ? "font-medium text-status-warning-fg"
                          : "text-muted-foreground",
                      )}
                    >
                      {checkDurationLabel(check)}
                    </TableCell>
                    {hasActions && (
                      <TableCell onClick={(e) => e.stopPropagation()}>
                        {check.status === "open" && (
                          <div className="flex gap-2">
                            {canClose && (
                              <Button
                                variant="outline"
                                size="sm"
                                onClick={() => handleClose(check.id, check.table_label)}
                                disabled={closeCheck.isPending}
                              >
                                {t("close")}
                              </Button>
                            )}
                            {canCancel && (
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => setCancelTarget(check)}
                                disabled={cancelCheck.isPending}
                                className="text-destructive hover:text-destructive"
                              >
                                {t("cancel")}
                              </Button>
                            )}
                          </div>
                        )}
                      </TableCell>
                    )}
                    <TableCell className="text-muted-foreground">
                      <ChevronRight className="size-4" aria-hidden />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <ConfirmDialog
        open={cancelTarget !== null}
        onOpenChange={(open) => !open && setCancelTarget(null)}
        title={t("cancelConfirm.title", { table: cancelTarget?.table_label ?? "" })}
        description={t("cancelConfirm.description")}
        confirmLabel={t("cancelConfirm.confirm")}
        cancelLabel={t("cancelConfirm.keep")}
        destructive
        onConfirm={async () => {
          if (cancelTarget) await handleCancel(cancelTarget)
          setCancelTarget(null)
        }}
      />
    </div>
  )
}
