"use client"

import { ChevronRightIcon } from "lucide-react"
import { useFormatter, useTranslations } from "next-intl"
import Link from "next/link"

import { Button, Skeleton } from "@onlinemenu/ui-kit"

import { ErrorState, RetryButton, useProblemMessage } from "@/components/error-state"
import { OrderStatusBadge } from "@/components/orders/order-status-badge"
import { useMyOrders } from "@/hooks/use-storefront"
import { toProblem } from "@/lib/api"
import { formatKurus } from "@/lib/money"

export function OrdersScreen() {
  const t = useTranslations("orders")
  const tCommon = useTranslations("common")
  const tErrors = useTranslations("errors")
  const format = useFormatter()
  const problemMessage = useProblemMessage()

  const { data: orders, isPending, isError, error, refetch } = useMyOrders()

  if (isPending) {
    return (
      <div className="flex flex-col gap-2 py-4" aria-busy="true">
        <Skeleton className="h-20 w-full rounded-xl" />
        <Skeleton className="h-20 w-full rounded-xl" />
      </div>
    )
  }

  if (isError) {
    return (
      <ErrorState
        title={tErrors("ordersLoadFailed")}
        message={problemMessage(toProblem(error))}
        action={<RetryButton label={tCommon("retry")} onRetry={() => void refetch()} />}
      />
    )
  }

  if (orders.length === 0) {
    return (
      <div className="py-16 text-center">
        <p className="text-base font-medium">{t("empty")}</p>
        <p className="text-muted-foreground mt-1 text-sm">{t("emptyHint")}</p>
        <Button asChild size="touch" variant="outline" className="mt-6">
          <Link href="/menu">{tCommon("back")}</Link>
        </Button>
      </div>
    )
  }

  return (
    <div className="py-4">
      <h1 className="mb-3 text-lg font-semibold">{t("title")}</h1>
      <ul className="flex flex-col gap-2">
        {orders.map((order) => (
          <li key={order.order_id}>
            <Link
              href={`/orders/${order.order_id}`}
              className="bg-card active:bg-accent flex min-h-16 items-center gap-3 rounded-xl border p-3 transition-colors"
            >
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <OrderStatusBadge status={order.status} />
                  <span className="text-muted-foreground truncate text-xs">
                    {format.dateTime(new Date(order.created_at), "short")}
                  </span>
                </div>
                <span className="mt-1 block text-sm font-semibold tabular-nums">
                  {formatKurus(order.total)}
                </span>
              </div>
              <ChevronRightIcon className="text-muted-foreground size-5" aria-hidden="true" />
            </Link>
          </li>
        ))}
      </ul>
    </div>
  )
}
