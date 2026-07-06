"use client"

import { Users } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useChecks } from "@/hooks/use-pos"
import { cn } from "@/lib/utils"
import { formatCheckTotal, formatOpenDuration, isLongOpenCheck } from "@/lib/pos-format"
import type { Check, CheckStatus } from "@/types"

function statusBadgeClass(status: CheckStatus): string {
  switch (status) {
    case "open":
      return "bg-amber-100 text-amber-700 border-amber-200 hover:bg-amber-100"
    case "closed":
      return "bg-green-100 text-green-700 border-green-200 hover:bg-green-100"
    case "cancelled":
      return "bg-gray-100 text-gray-600 border-gray-200 hover:bg-gray-100"
  }
}

function statusLabel(status: CheckStatus): string {
  switch (status) {
    case "open":
      return "Açık"
    case "closed":
      return "Kapalı"
    case "cancelled":
      return "İptal"
  }
}

// openDurationLabel mirrors the checks list's rule: elapsed-to-now for an
// open check, elapsed opened_at -> closed_at once it's done — never a
// still-growing duration for a check that's no longer open.
function openDurationLabel(check: Check): string {
  if (check.status === "open") return formatOpenDuration(check.opened_at)
  if (!check.closed_at) return "—"
  return formatOpenDuration(check.opened_at, new Date(check.closed_at))
}

export default function TablesPage() {
  const { data, isLoading } = useChecks({ refetchInterval: 30_000 })

  const checks = data ?? []
  const openCount = checks.filter((c) => c.status === "open").length
  const closedCount = checks.filter((c) => c.status === "closed").length

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Masalar</h1>
        <p className="text-muted-foreground">
          Masa durumlarını gerçek zamanlı takip edin.
        </p>
      </div>

      {!isLoading && checks.length > 0 && (
        <div className="flex items-center gap-4 text-sm">
          <div className="flex items-center gap-1.5">
            <div className="size-3 rounded-full bg-amber-500" />
            <span className="text-muted-foreground">Açık ({openCount})</span>
          </div>
          <div className="flex items-center gap-1.5">
            <div className="size-3 rounded-full bg-green-500" />
            <span className="text-muted-foreground">Kapalı ({closedCount})</span>
          </div>
        </div>
      )}

      {isLoading ? (
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-28 rounded-lg" />
          ))}
        </div>
      ) : checks.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-16 text-center">
          <p className="text-muted-foreground">Aktif adisyon yok</p>
        </div>
      ) : (
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
          {checks.map((check) => (
            <Card
              key={check.id}
              className="cursor-pointer hover:shadow-md transition-shadow py-0"
            >
              <CardHeader className="pb-2 pt-4 px-4">
                <CardTitle className="text-sm font-semibold">
                  {check.table_label}
                </CardTitle>
              </CardHeader>
              <CardContent className="pb-4 px-4">
                <div className="flex flex-col gap-1.5">
                  <Badge
                    className={statusBadgeClass(check.status)}
                    variant="outline"
                  >
                    {statusLabel(check.status)}
                  </Badge>

                  <div className="flex items-center justify-between text-xs tabular-nums">
                    <span className="inline-flex items-center gap-1 text-muted-foreground">
                      <Users className="size-3.5" />
                      {check.pax}
                    </span>
                    <span className="font-semibold">{formatCheckTotal(check.total)}</span>
                  </div>

                  <span
                    className={cn(
                      "text-xs tabular-nums",
                      check.status === "open" && isLongOpenCheck(check.opened_at)
                        ? "font-medium text-amber-600"
                        : "text-muted-foreground",
                    )}
                  >
                    {openDurationLabel(check)}
                  </span>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  )
}
