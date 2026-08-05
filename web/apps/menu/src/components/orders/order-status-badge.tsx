"use client"

import { useTranslations } from "next-intl"

import { Badge } from "@onlinemenu/ui-kit"

import { ORDER_STATUSES, type OrderStatus } from "@/types/storefront"

type StatusKey = OrderStatus | "unknown"

const BADGE_VARIANTS: Record<StatusKey, "default" | "secondary" | "destructive" | "success" | "warning" | "outline"> = {
  pending: "warning",
  accepted: "default",
  preparing: "default",
  ready: "success",
  delivered: "secondary",
  rejected: "destructive",
  cancelled: "secondary",
  unknown: "outline",
}

// A status the backend adds later must not render as a raw identifier on a
// diner's phone, so anything unrecognised collapses to a neutral "unknown".
function asStatusKey(status: string): StatusKey {
  return (ORDER_STATUSES as readonly string[]).includes(status)
    ? (status as OrderStatus)
    : "unknown"
}

export function OrderStatusBadge({ status }: { status: string }) {
  const t = useTranslations("orders.status")
  const key = asStatusKey(status)
  return <Badge variant={BADGE_VARIANTS[key]}>{t(key)}</Badge>
}

export function OrderStatusHint({ status }: { status: string }) {
  const t = useTranslations("orders.statusHint")
  const hint = t(asStatusKey(status))
  if (hint === "") return null
  return <p className="text-muted-foreground text-sm">{hint}</p>
}
