"use client"

import { CheckCircle2Icon } from "lucide-react"
import { useFormatter, useTranslations } from "next-intl"
import Link from "next/link"
import { useSearchParams } from "next/navigation"

import { Button, Skeleton } from "@onlinemenu/ui-kit"

import { ErrorState, RetryButton, useProblemMessage } from "@/components/error-state"
import { OrderStatusBadge, OrderStatusHint } from "@/components/orders/order-status-badge"
import { useOrder } from "@/hooks/use-storefront"
import { API_ERROR_CODES, toProblem } from "@/lib/api"
import { formatKurus } from "@/lib/money"

export function OrderDetail({ orderId }: { orderId: string }) {
  const t = useTranslations("orders")
  const tConfirm = useTranslations("confirmation")
  const tCommon = useTranslations("common")
  const format = useFormatter()
  const problemMessage = useProblemMessage()
  const searchParams = useSearchParams()

  // `?placed=1` is set by the redirect after a successful submission. The
  // confirmation is a banner on the status page rather than its own screen:
  // one fewer tap between "I ordered" and "where is it".
  const justPlaced = searchParams.get("placed") === "1"

  const { data: order, isPending, isError, error, refetch } = useOrder(orderId)

  if (isPending) {
    return (
      <div className="flex flex-col gap-3 py-6" aria-busy="true">
        <Skeleton className="h-8 w-40" />
        <Skeleton className="h-24 w-full rounded-xl" />
      </div>
    )
  }

  if (isError) {
    const problem = toProblem(error)
    if (problem.code === API_ERROR_CODES.notFound) {
      return (
        <ErrorState
          title={t("notFound")}
          message={t("notFoundHint")}
          action={
            <Button asChild size="touch" variant="outline">
              <Link href="/menu">{tConfirm("backToMenu")}</Link>
            </Button>
          }
        />
      )
    }
    return (
      <ErrorState
        title={t("detailTitle")}
        message={problemMessage(problem)}
        action={<RetryButton label={tCommon("retry")} onRetry={() => void refetch()} />}
      />
    )
  }

  return (
    <div className="flex flex-col gap-4 py-4">
      {justPlaced ? (
        <div className="flex items-start gap-3 rounded-xl border border-emerald-200 bg-emerald-50 p-3">
          <CheckCircle2Icon className="mt-0.5 size-5 shrink-0 text-emerald-600" aria-hidden="true" />
          <div>
            <p className="font-medium text-emerald-900">{tConfirm("title")}</p>
            <p className="mt-0.5 text-sm text-emerald-800">{tConfirm("description")}</p>
          </div>
        </div>
      ) : null}

      <div className="flex items-center gap-3">
        <h1 className="text-lg font-semibold">{t("detailTitle")}</h1>
        <OrderStatusBadge status={order.status} />
      </div>
      <OrderStatusHint status={order.status} />

      <p className="text-muted-foreground text-sm">
        {t("placedAt", { time: format.dateTime(new Date(order.created_at), "short") })}
      </p>

      <section className="bg-card rounded-xl border">
        <h2 className="border-b px-4 py-3 text-sm font-semibold">{t("items")}</h2>
        <ul className="divide-y">
          {order.items.map((item, index) => (
            <li key={`${item.name}-${index}`} className="flex items-start gap-3 px-4 py-3">
              <span className="text-muted-foreground w-8 shrink-0 text-sm tabular-nums">
                {format.number(item.quantity)}
              </span>
              <div className="min-w-0 flex-1">
                <span className="block text-sm font-medium">{item.name}</span>
                {item.note === "" ? null : (
                  <p className="text-muted-foreground mt-0.5 text-sm">{item.note}</p>
                )}
              </div>
              <span className="text-sm tabular-nums">
                {formatKurus(item.unit_price_amount * item.quantity)}
              </span>
            </li>
          ))}
        </ul>
        <div className="flex items-baseline justify-between border-t px-4 py-3">
          <span className="text-sm font-medium">{t("total")}</span>
          <span className="text-base font-semibold tabular-nums">{formatKurus(order.total)}</span>
        </div>
      </section>

      {order.note === "" ? null : (
        <section>
          <h2 className="text-sm font-semibold">{t("note")}</h2>
          <p className="text-muted-foreground mt-1 text-sm">{order.note}</p>
        </section>
      )}

      <Button asChild size="touch" variant="outline">
        <Link href="/menu">{tConfirm("backToMenu")}</Link>
      </Button>
    </div>
  )
}
