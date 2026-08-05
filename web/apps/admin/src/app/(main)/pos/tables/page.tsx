"use client"

import { QrCode, Users } from "lucide-react"
import { useTranslations } from "next-intl"
import { useEffect, useState } from "react"

import { QRCodeDialog } from "@/components/storefront/qr-code-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Select, SelectItem } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { useTables } from "@/hooks/use-pos"
import { useBranches } from "@/hooks/use-tenant"
import { useAuthStore } from "@/store/auth-store"
import type { PosTableStatus } from "@/types"

function statusBadgeClass(status: PosTableStatus): string {
  switch (status) {
    case "empty":
      return "bg-gray-100 text-gray-600 border-gray-200 hover:bg-gray-100"
    case "occupied":
      return "bg-amber-100 text-amber-700 border-amber-200 hover:bg-amber-100"
    case "reserved":
      return "bg-blue-100 text-blue-700 border-blue-200 hover:bg-blue-100"
    case "cleaning":
      return "bg-purple-100 text-purple-700 border-purple-200 hover:bg-purple-100"
  }
}

interface SelectedTable {
  id: string
  label: string
}

export default function TablesPage() {
  const t = useTranslations("posTables")
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""
  const { data: branches } = useBranches(tenantId)
  const [branchId, setBranchId] = useState("")
  const [selectedTable, setSelectedTable] = useState<SelectedTable | null>(null)

  useEffect(() => {
    if (!branchId && branches && branches.length > 0) {
      setBranchId(branches[0].id)
    }
  }, [branches, branchId])

  // This page is table-backed, not check-backed: a QR code is issued per table
  // and its whole point is to let a guest at an EMPTY table start ordering.
  // Driving the grid off open checks would make it impossible to print a code
  // for any table that has no check yet.
  //
  // Deliberate scope change: the previous check-backed version showed each
  // check's pax, total and open duration. Those are per-check figures with no
  // table-row equivalent (a table carries only `active_check_id`), so they now
  // live on the Adisyonlar page alone; this page reports whether a table has an
  // open check, not how much is on it.
  const { data: zones, isLoading } = useTables(branchId, { refetchInterval: 30_000 })

  const tableCount = (zones ?? []).reduce((sum, z) => sum + z.tables.length, 0)

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("title")}</h1>
          <p className="text-muted-foreground">{t("subtitle")}</p>
        </div>

        <div className="w-56 space-y-1">
          <label className="text-sm font-medium" htmlFor="branch-select">
            {t("branch")}
          </label>
          <Select
            id="branch-select"
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
      </div>

      {branchId === "" ? (
        <div className="flex flex-col items-center justify-center py-16 text-center">
          <p className="text-muted-foreground">{t("noBranch")}</p>
        </div>
      ) : isLoading ? (
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-32 rounded-lg" />
          ))}
        </div>
      ) : tableCount === 0 ? (
        <div className="flex flex-col items-center justify-center py-16 text-center">
          <p className="text-muted-foreground">{t("empty")}</p>
        </div>
      ) : (
        (zones ?? []).map((zone) => (
          <section key={zone.zone_id} className="space-y-3">
            <div className="flex items-baseline gap-2">
              <h2 className="text-lg font-semibold">{zone.zone_name}</h2>
              <span className="text-xs text-muted-foreground">
                {t("floor", { floor: zone.floor })}
              </span>
            </div>

            <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
              {zone.tables.map((table) => (
                <Card key={table.id} className="py-0">
                  <CardHeader className="px-4 pt-4 pb-2">
                    <CardTitle className="text-sm font-semibold">{table.name}</CardTitle>
                  </CardHeader>
                  <CardContent className="space-y-2 px-4 pb-4">
                    <div className="flex flex-wrap gap-1.5">
                      <Badge className={statusBadgeClass(table.status)} variant="outline">
                        {t(`status.${table.status}`)}
                      </Badge>
                      {/* `occupied` is set by manual status changes too, so it
                          is not the same claim as "there is a check to collect
                          money on" — the open check is surfaced separately. */}
                      {table.active_check_id && (
                        <Badge
                          variant="outline"
                          className="border-amber-200 bg-amber-50 text-amber-700"
                        >
                          {t("hasOpenCheck")}
                        </Badge>
                      )}
                    </div>

                    <div className="flex items-center gap-1 text-xs text-muted-foreground">
                      <Users className="size-3.5" />
                      {t("capacity", { count: table.capacity })}
                    </div>

                    <Button
                      variant="outline"
                      size="sm"
                      className="w-full"
                      onClick={() => setSelectedTable({ id: table.id, label: table.name })}
                    >
                      <QrCode className="size-3.5" />
                      {t("qrAction")}
                    </Button>
                  </CardContent>
                </Card>
              ))}
            </div>
          </section>
        ))
      )}

      {selectedTable && (
        // Keyed on the table id so switching tables remounts the dialog: its
        // in-memory raw token must never survive into another table's view.
        <QRCodeDialog
          key={selectedTable.id}
          open
          onOpenChange={(next) => {
            if (!next) setSelectedTable(null)
          }}
          branchId={branchId}
          tableId={selectedTable.id}
          tableLabel={selectedTable.label}
        />
      )}
    </div>
  )
}
