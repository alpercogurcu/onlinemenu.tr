"use client"

import { ChevronDown, ExternalLink } from "lucide-react"
import { useTranslations } from "next-intl"
import Link from "next/link"
import { useState } from "react"

import { formatMoney } from "@onlinemenu/pos-core"

import { Badge } from "@/components/ui/badge"
import { useCheckOrders } from "@/hooks/use-pos"
import { orderStatusVariant } from "@/lib/status-badge"
import { cn } from "@/lib/utils"
import type { OrderStatus } from "@/types"

// Same rule as the check detail screen: rejected/cancelled orders are not on
// the bill, so they do not count toward the "on the table" total either.
const NOT_BILLED: ReadonlySet<OrderStatus> = new Set(["rejected", "cancelled"])

/**
 * What the table already ordered, folded by default: the waiter's job here is
 * the NEW round, but "did I already send the ayran?" must be one tap away.
 */
export function SentItems({ checkId }: { checkId: string }) {
  const t = useTranslations("posOrder.sent")
  const [open, setOpen] = useState(false)
  const { data: orders } = useCheckOrders(checkId)

  const items = (orders ?? []).flatMap((order) =>
    order.items.map((item) => ({ ...item, status: order.status, orderId: order.id })),
  )
  const billed = items.filter((i) => !NOT_BILLED.has(i.status))
  const count = billed.length
  const total = billed.reduce((sum, i) => sum + i.quantity * i.unit_price_amount, 0)

  return (
    <section className="bg-card rounded-2xl border-2" data-testid="sent-items">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex min-h-14 w-full items-center gap-3 px-4 text-left outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
      >
        <span className="flex-1">
          <span className="block text-base font-semibold">{t("title")}</span>
          <span className="block text-sm text-muted-foreground tabular-nums">
            {items.length === 0 ? t("empty") : t("summary", { count, total: formatMoney(total) })}
          </span>
        </span>
        <ChevronDown
          className={cn("size-5 shrink-0 transition-transform motion-reduce:transition-none", open && "rotate-180")}
          aria-hidden="true"
        />
      </button>
      {open && (
        <div className="border-t px-4 pb-3">
          <ul className="divide-y">
            {items.map((item) => (
              <li
                key={`${item.orderId}-${item.id}`}
                className={cn("flex items-start gap-3 py-2", NOT_BILLED.has(item.status) && "opacity-60")}
              >
                <span className="w-8 shrink-0 text-base font-bold tabular-nums">{item.quantity}×</span>
                <span className="min-w-0 flex-1">
                  <span className="block text-base break-words">{item.product_name}</span>
                  {item.note && <span className="block text-sm break-words text-muted-foreground">{item.note}</span>}
                </span>
                <Badge variant={orderStatusVariant(item.status)} className="shrink-0">
                  {t(`status.${item.status}`)}
                </Badge>
              </li>
            ))}
          </ul>
          <Link
            href={`/pos/checks/${checkId}`}
            className="text-primary mt-1 inline-flex min-h-12 items-center gap-1.5 text-base font-medium underline-offset-4 hover:underline"
          >
            <ExternalLink className="size-4" aria-hidden="true" />
            {t("detail")}
          </Link>
        </div>
      )}
    </section>
  )
}
