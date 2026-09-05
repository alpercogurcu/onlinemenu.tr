"use client"

import { useTranslations } from "next-intl"
import { useEffect, useMemo, useState } from "react"
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"

import { CashSessionsCard } from "@/components/dashboard/cash-sessions-card"
import { PaymentsTable } from "@/components/dashboard/payments-table"
import { PeriodPicker } from "@/components/dashboard/period-picker"
import { SalesCards } from "@/components/dashboard/sales-cards"
import { TaxTable } from "@/components/dashboard/tax-table"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Select, SelectItem } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { useCan } from "@/hooks/use-can"
import { useProducts } from "@/hooks/use-catalog"
import { useChecks } from "@/hooks/use-pos"
import { periodRange, useSaleDetails, type ReportPeriod } from "@/hooks/use-reports"
import { useBranches } from "@/hooks/use-tenant"
import { formatKurus } from "@/lib/money"
import { useAuthStore } from "@/store/auth-store"

// dd.MM label for the AreaChart x-axis — by_day's `date` is a plain
// YYYY-MM-DD string (no time/zone component: it is already the backend's
// tenant-local business day), so a substring split is enough and avoids the
// timezone-shift risk of routing it through `new Date(...)`.
function dayLabel(isoDate: string): string {
  const [, month, day] = isoDate.split("-")
  return `${day}.${month}`
}

export default function DashboardClient() {
  const t = useTranslations("dashboard")
  const canViewReport = useCan("pos.report.read")

  const tenantId = useAuthStore((s) => s.tenantId) ?? ""
  const { data: branches } = useBranches(tenantId)
  const [branchId, setBranchId] = useState("")
  const [period, setPeriod] = useState<ReportPeriod>("today")

  useEffect(() => {
    if (!branchId && branches && branches.length > 0) {
      setBranchId(branches[0].id)
    }
  }, [branches, branchId])

  // Recomputed on every render (cheap: two Date allocations) rather than
  // memoized on `period` alone, so a preset held open past local midnight
  // (e.g. "today" left selected overnight) tracks the new calendar day
  // instead of freezing at the range computed when it was first selected.
  const { from, to } = useMemo(() => periodRange(period), [period])

  const openChecks = useChecks({ status: "open", limit: 100 })
  const allProducts = useProducts({ limit: 5 })
  const report = useSaleDetails({ branchId, from, to })

  const openCheckCount = openChecks.data?.length ?? 0
  const productCount = allProducts.data?.length ?? 0

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">{t("title")}</h1>
        <p className="text-muted-foreground">{t("subtitle")}</p>
      </div>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader>
            <CardDescription>{t("openChecks")}</CardDescription>
            {openChecks.isLoading ? (
              <Skeleton className="h-9 w-16" />
            ) : (
              <CardTitle className="text-3xl">{openCheckCount}</CardTitle>
            )}
          </CardHeader>
          <CardContent>
            <p className="text-xs text-muted-foreground">{t("openChecksHint")}</p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardDescription>{t("totalProducts")}</CardDescription>
            {allProducts.isLoading ? (
              <Skeleton className="h-9 w-16" />
            ) : (
              <CardTitle className="text-3xl">{productCount}</CardTitle>
            )}
          </CardHeader>
          <CardContent>
            <p className="text-xs text-muted-foreground">{t("totalProductsHint")}</p>
          </CardContent>
        </Card>
      </div>

      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="w-56 space-y-1">
          <label className="text-sm font-medium" htmlFor="dashboard-branch-select">
            {t("branch")}
          </label>
          <Select
            id="dashboard-branch-select"
            value={branchId}
            onValueChange={setBranchId}
            disabled={!branches || branches.length === 0}
          >
            <SelectItem value="">{t("branchPlaceholder")}</SelectItem>
            {(branches ?? []).map((branch) => (
              <SelectItem key={branch.id} value={branch.id}>
                {branch.name}
              </SelectItem>
            ))}
          </Select>
        </div>

        <PeriodPicker value={period} onChange={setPeriod} />
      </div>

      {!canViewReport ? (
        <p className="text-sm text-muted-foreground">{t("noPermission")}</p>
      ) : branchId === "" ? (
        <p className="text-sm text-muted-foreground">{t("noBranch")}</p>
      ) : (
        <>
          <SalesCards data={report.data} isLoading={report.isLoading} />

          <Card>
            <CardHeader>
              <CardTitle>{t("chart.title")}</CardTitle>
              <CardDescription>{t("chart.description")}</CardDescription>
            </CardHeader>
            <CardContent>
              <ResponsiveContainer width="100%" height={300}>
                <AreaChart data={report.data?.by_day ?? []}>
                  <defs>
                    <linearGradient id="colorSatis" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="var(--color-primary)" stopOpacity={0.3} />
                      <stop offset="95%" stopColor="var(--color-primary)" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" className="stroke-border" />
                  <XAxis dataKey="date" tickFormatter={dayLabel} className="text-xs" />
                  <YAxis className="text-xs" />
                  <Tooltip
                    labelFormatter={dayLabel}
                    formatter={(value: number) => [formatKurus(value), t("chart.tooltipLabel")]}
                  />
                  <Area
                    type="monotone"
                    dataKey="gross"
                    stroke="var(--color-primary)"
                    strokeWidth={2}
                    fill="url(#colorSatis)"
                  />
                </AreaChart>
              </ResponsiveContainer>
            </CardContent>
          </Card>

          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle>{t("payments.title")}</CardTitle>
              </CardHeader>
              <CardContent>
                <PaymentsTable payments={report.data?.payments ?? []} />
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>{t("tax.title")}</CardTitle>
              </CardHeader>
              <CardContent>
                <TaxTable rows={report.data?.by_tax_rate ?? []} />
              </CardContent>
            </Card>
          </div>

          <CashSessionsCard sessions={report.data?.cash_sessions ?? []} />
        </>
      )}
    </div>
  )
}
