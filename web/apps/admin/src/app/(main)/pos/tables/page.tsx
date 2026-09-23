"use client"

import { LayoutGrid, Pencil, Plus, QrCode, ReceiptText, ShoppingBasket, Users } from "lucide-react"
import { useTranslations } from "next-intl"
import Link from "next/link"
import { useEffect, useState } from "react"
import { toast } from "sonner"

import { TableFormDialog } from "@/components/pos/table-form-dialog"
import { ZoneFormDialog } from "@/components/pos/zone-form-dialog"
import { QRCodeDialog } from "@/components/storefront/qr-code-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Select, SelectItem } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { useCan } from "@/hooks/use-can"
import { currentBranchId } from "@/lib/permissions"
import { tableStatusVariant } from "@/lib/status-badge"
import { useSetTableStatus, useTables, useZones, type ManualTableStatus } from "@/hooks/use-pos"
import { useBranches } from "@/hooks/use-tenant"
import { useAuthStore } from "@/store/auth-store"
import type { PosTable, PosTableStatus, PosZone } from "@/types"

// Mirrors domain.allowedTableTransitions (backend/internal/modules/pos/domain/
// table.go) MINUS "occupied": TableService.SetStatus rejects that target
// outright, because a table may only become occupied as a side effect of
// opening a check. Offering a transition the server will 409 on would be a
// worse UI than not offering it, hence the duplicated table — if the backend
// machine changes, this list has to follow.
const MANUAL_TRANSITIONS: Record<PosTableStatus, ManualTableStatus[]> = {
  empty: ["reserved", "cleaning"],
  occupied: ["cleaning", "empty"],
  reserved: ["empty"],
  cleaning: ["empty"],
}

interface SelectedTable {
  id: string
  label: string
}

export default function TablesPage() {
  const t = useTranslations("posTables")
  // Cosmetic-only gates (see lib/permissions.ts): pos.table.read (this page)
  // is granted to cashier/shift_manager/waiter/kitchen/bar, but every action
  // on the page is narrower. A control the role cannot use is not rendered at
  // all — a visible button whose save always fails is what users reported.
  //   storefront.qr.manage — issue/rotate/revoke a table's code. The QR dialog
  //                          is useless with read alone: the raw token is shown
  //                          only when issued, so a cashier would meet two
  //                          greyed-out buttons. The button follows .manage.
  //   pos.table.manage    — zone/table CRUD and any status move (shift_manager)
  //   pos.table.clean     — only cleaning -> empty (cashier/shift_manager/waiter)
  const canViewQR = useCan("storefront.qr.manage")
  const canManage = useCan("pos.table.manage")
  const canClean = useCan("pos.table.clean")
  //   pos.order.place     — tapping a table opens the web order screen
  const canOrder = useCan("pos.order.place")
  //   pos.check.read      — an occupied table links to its adisyon
  const canReadChecks = useCan("pos.check.read")
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""
  const { data: branches } = useBranches(tenantId)
  // Non-null for a branch-scoped operator — they always work their own
  // branch, so the branch control below renders as static text for them
  // instead of a Select that would let them pick a branch they have no
  // access to and land on an empty/403'd board.
  const scopedBranchId = currentBranchId()
  const [branchId, setBranchId] = useState("")
  const [qrTable, setQrTable] = useState<SelectedTable | null>(null)
  const [zoneDialog, setZoneDialog] = useState<{ zone?: PosZone } | null>(null)
  const [tableDialog, setTableDialog] = useState<{ table?: PosTable; zoneId?: string } | null>(null)

  useEffect(() => {
    if (!branchId && branches && branches.length > 0) {
      const own = currentBranchId()
      setBranchId(own && branches.some((b) => b.id === own) ? own : branches[0].id)
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
  const { data: plan, isLoading } = useTables(branchId, { refetchInterval: 30_000 })
  // Zones are fetched separately and drive the section list: GET /tables builds
  // its sections by grouping table rows, so a freshly created zone with no
  // table in it would be invisible — the operator would create a zone and see
  // nothing happen. Rendering from /zones makes the empty zone appear with its
  // own "add the first table" prompt.
  const { data: zones } = useZones(branchId)
  const setStatus = useSetTableStatus()

  const tablesByZone = new Map<string, PosTable[]>()
  for (const section of plan ?? []) {
    tablesByZone.set(section.zone_id, section.tables)
  }
  const zoneList = zones ?? []
  const tableCount = (plan ?? []).reduce((sum, z) => sum + z.tables.length, 0)

  function transitionsFor(status: PosTableStatus): ManualTableStatus[] {
    if (canManage) return MANUAL_TRANSITIONS[status]
    if (canClean && status === "cleaning") return ["empty"]
    return []
  }

  async function handleStatusChange(table: PosTable, status: ManualTableStatus) {
    try {
      await setStatus.mutateAsync({ id: table.id, status })
      toast.success(t("statusChanged", { table: table.name, status: t(`status.${status}`) }))
    } catch {
      toast.error(t("statusChangeFailed"))
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("title")}</h1>
          <p className="text-muted-foreground">
            {canManage ? t("subtitle") : canOrder ? t("subtitleOrder") : t("subtitleView")}
          </p>
        </div>

        <div className="flex flex-wrap items-end gap-3">
          <div className="w-56 space-y-1">
            <span id="branch-label" className="text-sm font-medium">
              {t("branch")}
            </span>
            {scopedBranchId ? (
              <p aria-labelledby="branch-label" className="flex h-9 items-center text-sm font-medium">
                {branches?.find((b) => b.id === branchId)?.name ?? "—"}
              </p>
            ) : (
              <Select
                id="branch-select"
                aria-labelledby="branch-label"
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
            )}
          </div>

          {canManage && (
            <>
              <Button variant="outline" onClick={() => setZoneDialog({})} disabled={branchId === ""}>
                <LayoutGrid className="size-4" />
                {t("addZone")}
              </Button>
              <Button
                onClick={() => setTableDialog({})}
                disabled={branchId === "" || zoneList.length === 0}
              >
                <Plus className="size-4" />
                {t("addTable")}
              </Button>
            </>
          )}
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
      ) : zoneList.length === 0 ? (
        <div className="flex flex-col items-center justify-center gap-4 py-16 text-center">
          <p className="text-muted-foreground">{t("emptyZones")}</p>
          {canManage && (
            <Button onClick={() => setZoneDialog({})}>
              <LayoutGrid className="size-4" />
              {t("addFirstZone")}
            </Button>
          )}
        </div>
      ) : (
        <>
          {tableCount === 0 && <p className="text-sm text-muted-foreground">{t("empty")}</p>}

          {zoneList.map((zone) => {
            const zoneTables = tablesByZone.get(zone.id) ?? []
            return (
              <section key={zone.id} className="space-y-3">
                <div className="flex flex-wrap items-baseline gap-2">
                  <h2 className="text-lg font-semibold">{zone.name}</h2>
                  <span className="text-xs text-muted-foreground">
                    {t("floor", { floor: zone.floor })}
                  </span>
                  {!zone.is_active && (
                    <Badge variant="outline" className="text-muted-foreground">
                      {t("zonePassive")}
                    </Badge>
                  )}
                  {canManage && (
                    <>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => setZoneDialog({ zone })}
                        aria-label={t("editZoneAria", { zone: zone.name })}
                      >
                        <Pencil className="size-3.5" />
                        {t("editZone")}
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => setTableDialog({ zoneId: zone.id })}>
                        <Plus className="size-3.5" />
                        {t("addTable")}
                      </Button>
                    </>
                  )}
                </div>

                {zoneTables.length === 0 ? (
                  <p className="text-sm text-muted-foreground">{t("zoneEmpty")}</p>
                ) : (
                  <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
                    {zoneTables.map((table) => {
                      const summary = (
                        <>
                          <CardHeader className="px-4 pt-4 pb-2">
                            <CardTitle className="text-sm font-semibold">{table.name}</CardTitle>
                          </CardHeader>
                          <CardContent className="space-y-2 px-4 pb-2">
                            <div className="flex flex-wrap gap-1.5">
                              <Badge variant={tableStatusVariant(table.status)}>
                                {t(`status.${table.status}`)}
                              </Badge>
                              {/* `occupied` is set by manual status changes too, so it
                                  is not the same claim as "there is a check to collect
                                  money on" — the open check is surfaced separately. */}
                              {table.active_check_id && (
                                <Badge
                                  variant="outline"
                                  className="border-status-warning-border bg-status-warning-bg text-status-warning-fg"
                                >
                                  {t("hasOpenCheck")}
                                </Badge>
                              )}
                            </div>

                            <div className="flex items-center gap-1 text-xs text-muted-foreground">
                              <Users className="size-3.5" />
                              {t("capacity", { count: table.capacity })}
                            </div>
                          </CardContent>
                        </>
                      )
                      return (
                        <Card key={table.id} className="gap-0 overflow-hidden py-0">
                          {/* Whoever may take an order (garson/kasiyer/yönetici)
                              taps the table itself to order; the management
                              controls below stay outside the tap target so a
                              status change can never be mistaken for it. */}
                          {canOrder ? (
                            <Link
                              href={`/pos/order?${new URLSearchParams({ branch: branchId, table: table.id }).toString()}`}
                              aria-label={t("orderAria", { table: table.name })}
                              className="hover:bg-accent/60 focus-visible:ring-ring/50 block outline-none focus-visible:ring-[3px] focus-visible:ring-inset"
                            >
                              {summary}
                              <span className="text-primary flex items-center gap-1 px-4 pb-3 text-sm font-semibold">
                                <ShoppingBasket className="size-4" aria-hidden="true" />
                                {t("orderAction")}
                              </span>
                            </Link>
                          ) : (
                            summary
                          )}

                          {canReadChecks && table.active_check_id && (
                            <Link
                              href={`/pos/checks/${table.active_check_id}`}
                              aria-label={t("openCheckAria", { table: table.name })}
                              className="mx-4 mb-2 flex min-h-11 items-center justify-center gap-1 rounded-md border text-sm font-medium hover:bg-accent/60"
                            >
                              <ReceiptText className="size-4" aria-hidden="true" />
                              {t("openCheck")}
                            </Link>
                          )}

                          {(transitionsFor(table.status).length > 0 || canViewQR || canManage) && (
                            <CardContent className="space-y-2 px-4 pt-2 pb-4">
                              {transitionsFor(table.status).length > 0 && (
                                <Select
                                  aria-label={t("statusChangeAria", { table: table.name })}
                                  className="h-8 text-xs"
                                  value=""
                                  disabled={setStatus.isPending}
                                  onValueChange={(next) => {
                                    if (next === "") return
                                    void handleStatusChange(table, next as ManualTableStatus)
                                  }}
                                >
                                  <SelectItem value="">{t("statusChange")}</SelectItem>
                                  {transitionsFor(table.status).map((next) => (
                                    <SelectItem key={next} value={next}>
                                      {t(`status.${next}`)}
                                    </SelectItem>
                                  ))}
                                </Select>
                              )}

                              {(canViewQR || canManage) && (
                                <div className="flex gap-2">
                                  {canViewQR && (
                                    <Button
                                      variant="outline"
                                      size="sm"
                                      className="flex-1"
                                      onClick={() => setQrTable({ id: table.id, label: table.name })}
                                    >
                                      <QrCode className="size-3.5" />
                                      {t("qrAction")}
                                    </Button>
                                  )}
                                  {canManage && (
                                    <Button
                                      variant="ghost"
                                      size="sm"
                                      onClick={() => setTableDialog({ table })}
                                      aria-label={t("editTableAria", { table: table.name })}
                                    >
                                      <Pencil className="size-3.5" />
                                    </Button>
                                  )}
                                </div>
                              )}
                            </CardContent>
                          )}
                        </Card>
                      )
                    })}
                  </div>
                )}
              </section>
            )
          })}
        </>
      )}

      {zoneDialog && (
        // Keyed so the form state is rebuilt per target: the dialog seeds its
        // fields from props on mount only.
        <ZoneFormDialog
          key={zoneDialog.zone?.id ?? "new-zone"}
          open
          onOpenChange={(next) => {
            if (!next) setZoneDialog(null)
          }}
          branchId={branchId}
          zone={zoneDialog.zone}
        />
      )}

      {tableDialog && (
        <TableFormDialog
          key={tableDialog.table?.id ?? `new-table-${tableDialog.zoneId ?? ""}`}
          open
          onOpenChange={(next) => {
            if (!next) setTableDialog(null)
          }}
          branchId={branchId}
          zones={zoneList}
          table={tableDialog.table}
          defaultZoneId={tableDialog.zoneId}
        />
      )}

      {qrTable && (
        // Keyed on the table id so switching tables remounts the dialog: its
        // in-memory raw token must never survive into another table's view.
        <QRCodeDialog
          key={qrTable.id}
          open
          onOpenChange={(next) => {
            if (!next) setQrTable(null)
          }}
          branchId={branchId}
          tableId={qrTable.id}
          tableLabel={qrTable.label}
        />
      )}
    </div>
  )
}
