"use client"

import { useTranslations } from "next-intl"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { formatKurus } from "@/lib/money"
import type { SaleDetails } from "@/types"

// Keeps the existing 4-card grid look from dashboard-client.tsx (the
// hardcoded-data version this replaces) — same Card/CardHeader/CardDescription/
// CardTitle/CardContent structure and skeleton-while-loading behaviour, only
// fed by the real report now instead of mockSalesData.
export function SalesCards({ data, isLoading }: { data: SaleDetails | undefined; isLoading: boolean }) {
  const t = useTranslations("dashboard")

  return (
    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
      <Card>
        <CardHeader>
          <CardDescription>{t("sales")}</CardDescription>
          {isLoading ? (
            <Skeleton className="h-9 w-24" />
          ) : (
            <CardTitle className="text-3xl">{formatKurus(data?.sales.gross)}</CardTitle>
          )}
        </CardHeader>
        <CardContent>
          <p className="text-xs text-muted-foreground">{t("salesHint")}</p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardDescription>{t("closedChecks")}</CardDescription>
          {isLoading ? (
            <Skeleton className="h-9 w-16" />
          ) : (
            <CardTitle className="text-3xl">{data?.sales.closed_check_count ?? 0}</CardTitle>
          )}
        </CardHeader>
        <CardContent>
          <p className="text-xs text-muted-foreground">{t("closedChecksHint")}</p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardDescription>{t("averageCheck")}</CardDescription>
          {isLoading ? (
            <Skeleton className="h-9 w-24" />
          ) : (
            <CardTitle className="text-3xl">{formatKurus(data?.sales.average_check)}</CardTitle>
          )}
        </CardHeader>
        <CardContent>
          <p className="text-xs text-muted-foreground">{t("averageCheckHint")}</p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardDescription>{t("cancellations")}</CardDescription>
          {isLoading ? (
            <Skeleton className="h-9 w-24" />
          ) : (
            <CardTitle className="text-3xl">{formatKurus(data?.cancellations.amount)}</CardTitle>
          )}
        </CardHeader>
        <CardContent>
          <p className="text-xs text-muted-foreground">
            {t("cancellationsHint", { count: data?.cancellations.check_count ?? 0 })}
          </p>
        </CardContent>
      </Card>
    </div>
  )
}
