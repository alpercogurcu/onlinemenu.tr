"use client"

import { ArrowLeft, Users } from "lucide-react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"

import Link from "next/link"
import type { ReactNode } from "react"

import { useBreadcrumbLabel } from "@/components/layouts/dynamic-breadcrumb"
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
import { useCan } from "@/hooks/use-can"
import {
  useCancelCheck,
  useCheck,
  useCheckOrders,
  useCheckSettlement,
  useCloseCheck,
} from "@/hooks/use-pos"
import { formatKurus } from "@/lib/money"
import { checkDurationLabel } from "@/lib/pos-format"
import { checkStatusVariant, orderStatusVariant } from "@/lib/status-badge"
import { cn } from "@/lib/utils"
import type { Check, CheckSettlement, Order, OrderStatus } from "@/types"

// Orders in these states never reach the bill (backend check total excludes
// them); they stay listed so the history is honest, but visibly set apart.
const NOT_BILLED: ReadonlySet<OrderStatus> = new Set(["rejected", "cancelled"])

function formatDateTime(iso: string): string {
  return new Date(iso).toLocaleString("tr-TR")
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString("tr-TR", { hour: "2-digit", minute: "2-digit" })
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="space-y-0.5">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-sm font-medium">{children}</dd>
    </div>
  )
}

function OrderCard({ order }: { order: Order }) {
  const t = useTranslations("posChecks")
  const billed = !NOT_BILLED.has(order.status)
  const orderTotal = order.items.reduce((sum, it) => sum + it.quantity * it.unit_price_amount, 0)

  return (
    <section
      aria-label={t("detail.orderAt", { time: formatTime(order.created_at) })}
      className={cn("rounded-lg border", !billed && "opacity-70")}
    >
      <header className="flex flex-wrap items-center gap-2 border-b px-4 py-2">
        <span className="text-sm font-semibold">
          {t("detail.orderAt", { time: formatTime(order.created_at) })}
        </span>
        <Badge variant={orderStatusVariant(order.status)}>{t(`orderStatus.${order.status}`)}</Badge>
        {order.order_channel && (
          <span className="text-xs text-muted-foreground">
            {t.has(`detail.channel.${order.order_channel}`)
              ? t(`detail.channel.${order.order_channel}`)
              : order.order_channel}
          </span>
        )}
        {!billed && <span className="text-xs text-muted-foreground">· {t("detail.notCounted")}</span>}
        <span className={cn("ml-auto text-sm font-medium tabular-nums", !billed && "line-through")}>
          {formatKurus(orderTotal)}
        </span>
      </header>
      {order.rejection_reason && (
        <p className="px-4 pt-2 text-xs text-destructive">
          {t("detail.rejectionReason", { reason: order.rejection_reason })}
        </p>
      )}
      {order.note && <p className="px-4 pt-2 text-xs text-muted-foreground">{order.note}</p>}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-16 text-right">{t("detail.colQty")}</TableHead>
            <TableHead>{t("detail.colProduct")}</TableHead>
            <TableHead className="text-right">{t("detail.colUnitPrice")}</TableHead>
            <TableHead className="text-right">{t("detail.colLineTotal")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {order.items.map((item) => {
            const optionCount = item.modifier_ids?.length ?? 0
            return (
              <TableRow key={item.id}>
                <TableCell className="text-right tabular-nums">{item.quantity}×</TableCell>
                <TableCell>
                  <div className="font-medium">{item.product_name}</div>
                  {(item.note || optionCount > 0) && (
                    <div className="text-xs text-muted-foreground">
                      {[item.note, optionCount > 0 ? t("detail.optionCount", { count: optionCount }) : ""]
                        .filter(Boolean)
                        .join(" · ")}
                    </div>
                  )}
                </TableCell>
                <TableCell className="text-right tabular-nums text-muted-foreground">
                  {formatKurus(item.unit_price_amount)}
                </TableCell>
                <TableCell className={cn("text-right font-medium tabular-nums", !billed && "line-through")}>
                  {formatKurus(item.quantity * item.unit_price_amount)}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </section>
  )
}

function Summary({
  check,
  settlement,
  settlementState,
}: {
  check: Check
  settlement: CheckSettlement | undefined
  settlementState: "hidden" | "loading" | "error" | "ready"
}) {
  const t = useTranslations("posChecks")
  const total = check.total ?? 0
  const paid = (settlement?.completed ?? []).reduce((sum, p) => sum + p.amount_total, 0)
  const pending = settlement?.pending_total ?? 0
  // Same rule as the POS till (pos-desktop lib/fiscalStatus collectableRemaining):
  // money in flight is already reserved, so it is NOT still owed — showing it
  // as "kalan" is exactly the misreading that led to double collection.
  const remaining = Math.max(total - paid - pending, 0)

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("detail.summaryTitle")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex items-baseline justify-between">
          <span className="text-sm text-muted-foreground">{t("detail.total")}</span>
          <span className="text-2xl font-bold tabular-nums">{formatKurus(check.total)}</span>
        </div>

        {settlementState === "loading" && <Skeleton className="h-16 w-full" />}
        {settlementState === "error" && (
          <p className="text-sm text-muted-foreground">{t("detail.paymentsUnavailable")}</p>
        )}
        {settlementState === "ready" && settlement && (
          <>
            <div className="flex justify-between text-sm">
              <span className="text-muted-foreground">{t("detail.paid")}</span>
              <span className="tabular-nums">{formatKurus(paid)}</span>
            </div>
            {pending > 0 && (
              <div className="flex justify-between text-sm">
                <span className="text-muted-foreground">{t("detail.pending")}</span>
                <span className="tabular-nums text-status-warning-fg">{formatKurus(pending)}</span>
              </div>
            )}
            <div className="flex items-baseline justify-between border-t pt-3">
              <span className="text-sm font-medium">{t("detail.remaining")}</span>
              <span className="text-lg font-semibold tabular-nums">{formatKurus(remaining)}</span>
            </div>
            <div className="space-y-1 pt-2">
              <p className="text-xs font-medium text-muted-foreground">{t("detail.paymentsTitle")}</p>
              {settlement.completed.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t("detail.noPayments")}</p>
              ) : (
                <ul className="space-y-1">
                  {settlement.completed.map((p) => (
                    <li key={p.payment_id} className="flex justify-between text-sm">
                      <span className="font-mono text-xs text-muted-foreground">
                        {p.payment_id.slice(0, 8)}
                      </span>
                      <span className="tabular-nums">{formatKurus(p.amount_total)}</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}

/**
 * Full-width adisyon detail (not a drawer: operators found drawers cramped).
 * Items, order states and check facts are visible to every role that may read
 * checks; the money state (payments, remaining) only to roles holding
 * payment.fiscal_status.read — the settlement query is not even issued
 * otherwise, so a waiter sees no 403 toast.
 */
export function CheckDetail({ checkId }: { checkId: string }) {
  const t = useTranslations("posChecks")
  const canSeeMoney = useCan("payment.fiscal_status.read")
  const canClose = useCan("pos.check.close")
  const canCancel = useCan("pos.check.cancel")

  const checkQuery = useCheck(checkId)
  const ordersQuery = useCheckOrders(checkId)
  const settlementQuery = useCheckSettlement(checkId, canSeeMoney)
  const closeCheck = useCloseCheck()
  const cancelCheck = useCancelCheck()

  const check = checkQuery.data
  useBreadcrumbLabel(check?.table_label)

  const orders = [...(ordersQuery.data ?? [])].sort((a, b) => a.created_at.localeCompare(b.created_at))

  const settlementState: "hidden" | "loading" | "error" | "ready" = !canSeeMoney
    ? "hidden"
    : settlementQuery.isLoading
      ? "loading"
      : settlementQuery.isError
        ? "error"
        : "ready"

  async function run(kind: "close" | "cancel") {
    if (!check) return
    try {
      if (kind === "close") {
        await closeCheck.mutateAsync(check.id)
        toast.success(t("closed", { table: check.table_label }))
      } else {
        await cancelCheck.mutateAsync(check.id)
        toast.success(t("cancelled", { table: check.table_label }))
      }
    } catch {
      toast.error(t(kind === "close" ? "closeFailed" : "cancelFailed"))
    }
  }

  const back = (
    <Button asChild variant="ghost" size="sm" className="-ml-2">
      <Link href="/pos/checks">
        <ArrowLeft className="size-4" />
        {t("detail.back")}
      </Link>
    </Button>
  )

  if (checkQuery.isLoading) {
    return (
      <div className="space-y-4">
        {back}
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    )
  }

  if (checkQuery.isError || !check) {
    return (
      <div className="space-y-4">
        {back}
        <p className="text-muted-foreground">{t("detail.notFound")}</p>
      </div>
    )
  }

  const isOpen = check.status === "open"

  return (
    <div className="space-y-6">
      {back}

      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold tracking-tight">{check.table_label}</h1>
            <Badge variant={checkStatusVariant(check.status)}>{t(`status.${check.status}`)}</Badge>
          </div>
          {check.note && <p className="text-sm text-muted-foreground">{check.note}</p>}
        </div>
        {isOpen && (canClose || canCancel) && (
          <div className="flex gap-2">
            {canClose && (
              <Button variant="outline" onClick={() => void run("close")} disabled={closeCheck.isPending}>
                {t("close")}
              </Button>
            )}
            {canCancel && (
              <Button
                variant="ghost"
                onClick={() => void run("cancel")}
                disabled={cancelCheck.isPending}
                className="text-destructive hover:text-destructive"
              >
                {t("cancel")}
              </Button>
            )}
          </div>
        )}
      </div>

      <dl className="grid grid-cols-2 gap-4 rounded-lg border p-4 sm:grid-cols-5">
        <Fact label={t("detail.table")}>{check.table_label}</Fact>
        <Fact label={t("detail.pax")}>
          <span className="inline-flex items-center gap-1">
            <Users className="size-3.5 text-muted-foreground" />
            {check.pax}
          </span>
        </Fact>
        <Fact label={t("detail.openedAt")}>{formatDateTime(check.opened_at)}</Fact>
        <Fact label={t("detail.closedAt")}>{check.closed_at ? formatDateTime(check.closed_at) : "—"}</Fact>
        <Fact label={t("detail.duration")}>{checkDurationLabel(check)}</Fact>
      </dl>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="space-y-3">
          <h2 className="text-lg font-semibold">{t("detail.ordersTitle")}</h2>
          {ordersQuery.isLoading ? (
            <Skeleton className="h-40 w-full" />
          ) : ordersQuery.isError ? (
            <p className="text-sm text-muted-foreground">{t("detail.loadFailed")}</p>
          ) : orders.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("detail.noOrders")}</p>
          ) : (
            orders.map((order) => <OrderCard key={order.id} order={order} />)
          )}
        </div>
        <div>
          <Summary check={check} settlement={settlementQuery.data} settlementState={settlementState} />
        </div>
      </div>
    </div>
  )
}
