"use client"

import { Users } from "lucide-react"
import { useTranslations } from "next-intl"
import { useState } from "react"
import { toast } from "sonner"

import { formatMoney } from "@onlinemenu/pos-core"

import { NewServiceCheckSheet, ServiceCheckList, type ServiceCheckFormValues, type ServiceKind } from "@/components/pos/order/service-checks"
import { TouchConfirm } from "@/components/pos/order/touch-sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { useTables } from "@/hooks/use-pos"
import { useCleanTable, useOpenChecks, useOpenServiceCheck, type OpenServiceCheckBody } from "@/hooks/use-pos-order"
import { describeOrderError } from "@/lib/pos-order"
import { statusSurfaceClass, tableStatusVariant } from "@/lib/status-badge"
import { cn } from "@/lib/utils"
import type { Check, PosTable } from "@/types"

type PickerMode = "tables" | ServiceKind

interface TablePickerProps {
  /** Selected in the global header switcher (use-selected-branch). */
  branchId: string
  onPick: (table: PosTable) => void
  /** A gel al / paket check was picked or just opened: go to its order screen. */
  onPickCheck: (check: Check) => void
}

/**
 * Step 1 of the waiter flow: which table — or which gel al / paket check.
 * Three segments ("Masalar | Gel Al | Paket", ≥44px targets): tables keep
 * their tile grid, tableless checks are customer-name rows because they have
 * no place in the floor plan. Zone tabs when there is more than one zone;
 * every tile carries its status as TEXT as well as colour (spec ilke 7), and
 * an occupied tile shows what is already on it. A table in cleaning cannot
 * take a new check (the server refuses it), so tapping one asks
 * "temizlendi mi?" and frees it first instead of failing later.
 */
export function TablePicker({ branchId, onPick, onPickCheck }: TablePickerProps) {
  const t = useTranslations("posOrder")
  const { data: plan, isLoading } = useTables(branchId, { refetchInterval: 20_000 })
  const { data: openChecks } = useOpenChecks(branchId)
  const clean = useCleanTable()
  const openService = useOpenServiceCheck()
  const [mode, setMode] = useState<PickerMode>("tables")
  const [zoneId, setZoneId] = useState<string | null>(null)
  const [cleaning, setCleaning] = useState<PosTable | null>(null)
  const [newService, setNewService] = useState<ServiceKind | null>(null)
  const [openError, setOpenError] = useState<string | null>(null)

  const zones = (plan ?? []).filter((z) => z.tables.some((table) => table.is_active))
  const activeZone = zones.find((z) => z.zone_id === zoneId) ?? zones[0]
  const totals = new Map((openChecks ?? []).map((c) => [c.id, c.total]))
  const serviceChecks = (serviceType: ServiceKind) =>
    (openChecks ?? []).filter((c) => c.service_type === serviceType)

  function handleTap(table: PosTable) {
    if (table.status === "cleaning" && !table.active_check_id) {
      setCleaning(table)
      return
    }
    onPick(table)
  }

  async function openServiceCheck(serviceType: ServiceKind, values: ServiceCheckFormValues) {
    const body: OpenServiceCheckBody = {
      branch_id: branchId,
      service_type: serviceType,
      customer_name: values.customer_name,
      ...(values.customer_phone ? { customer_phone: values.customer_phone } : {}),
      ...(values.customer_address ? { customer_address: values.customer_address } : {}),
    }
    try {
      const check = await openService.mutateAsync(body)
      setNewService(null)
      setOpenError(null)
      onPickCheck(check)
    } catch (err) {
      setOpenError(describeOrderError(err).message || t("service.openFailed"))
    }
  }

  const modes: { id: PickerMode; label: string }[] = [
    { id: "tables", label: t("tabs.tables") },
    { id: "takeaway", label: t("tabs.takeaway") },
    { id: "delivery", label: t("tabs.delivery") },
  ]

  return (
    <div className="max-w-5xl space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("pickTitle")}</h1>
          <p className="text-base text-muted-foreground">{t("pickSubtitle")}</p>
        </div>
      </div>

      {branchId === "" ? (
        <p className="py-12 text-center text-muted-foreground">{t("noBranch")}</p>
      ) : (
        <>
          <div role="tablist" aria-label={t("tabs.aria")} className="bg-card grid grid-cols-3 gap-1 rounded-2xl border-2 p-1">
            {modes.map(({ id, label }) => {
              const active = id === mode
              const count = id === "tables" ? 0 : serviceChecks(id).length
              return (
                <button
                  key={id}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  onClick={() => setMode(id)}
                  className={cn(
                    "flex min-h-12 items-center justify-center gap-1.5 rounded-xl text-base font-semibold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
                    active ? "bg-foreground text-background" : "text-foreground",
                  )}
                >
                  {label}
                  {count > 0 && (
                    <span
                      className={cn(
                        "min-w-6 rounded-full px-1.5 py-0.5 text-center text-sm font-bold tabular-nums",
                        active ? "bg-background/20" : "bg-muted",
                      )}
                    >
                      {count}
                    </span>
                  )}
                </button>
              )
            })}
          </div>

          {mode !== "tables" ? (
            <ServiceCheckList
              serviceType={mode}
              checks={serviceChecks(mode)}
              onPick={onPickCheck}
              onNew={() => {
                setOpenError(null)
                setNewService(mode)
              }}
            />
          ) : isLoading ? (
            <div className="grid grid-cols-3 gap-3 sm:grid-cols-4 lg:grid-cols-6">
              {Array.from({ length: 9 }).map((_, i) => (
                <Skeleton key={i} className="h-28 rounded-2xl" />
              ))}
            </div>
          ) : zones.length === 0 ? (
            <p className="py-12 text-center text-muted-foreground">{t("noTables")}</p>
          ) : (
            <>
              {zones.length > 1 && (
                <div role="tablist" aria-label={t("zonesAria")} className="-mx-4 flex gap-2 overflow-x-auto px-4 pb-1">
                  {zones.map((zone) => {
                    const active = zone.zone_id === activeZone?.zone_id
                    return (
                      <button
                        key={zone.zone_id}
                        type="button"
                        role="tab"
                        aria-selected={active}
                        onClick={() => setZoneId(zone.zone_id)}
                        className={cn(
                          "min-h-12 shrink-0 rounded-full border-2 px-5 text-base font-semibold whitespace-nowrap outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
                          active ? "border-foreground bg-foreground text-background" : "border-border bg-card",
                        )}
                      >
                        {zone.zone_name}
                      </button>
                    )
                  })}
                </div>
              )}

              <div className="grid grid-cols-3 gap-3 sm:grid-cols-4 lg:grid-cols-6" role="list">
                {(activeZone?.tables ?? [])
                  .filter((table) => table.is_active)
                  .map((table) => {
                    const status = t(`status.${table.status}`)
                    const total = table.active_check_id ? totals.get(table.active_check_id) : undefined
                    return (
                      <div role="listitem" key={table.id}>
                        <button
                          type="button"
                          onClick={() => handleTap(table)}
                          aria-label={t("tableAria", { table: table.name, status })}
                          data-testid="table-tile"
                          className={cn(
                            "flex min-h-28 w-full flex-col justify-between rounded-2xl border-2 p-3 text-left",
                            "touch-manipulation outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 active:scale-[0.97] motion-reduce:active:scale-100",
                            statusSurfaceClass(tableStatusVariant(table.status)),
                          )}
                        >
                          <span className="text-xl leading-tight font-bold break-words">{table.name}</span>
                          <span className="space-y-0.5">
                            <span className="block text-sm font-semibold">{status}</span>
                            {total ? (
                              <span className="block text-sm font-medium tabular-nums">{formatMoney(total)}</span>
                            ) : (
                              <span className="flex items-center gap-1 text-sm opacity-80">
                                <Users className="size-3.5" aria-hidden="true" />
                                {table.capacity}
                              </span>
                            )}
                          </span>
                        </button>
                      </div>
                    )
                  })}
              </div>
            </>
          )}
        </>
      )}

      {newService && (
        <NewServiceCheckSheet
          key={newService}
          serviceType={newService}
          pending={openService.isPending}
          error={openError}
          onOpenChange={(next) => {
            if (!next) {
              setNewService(null)
              setOpenError(null)
            }
          }}
          onSubmit={(values) => void openServiceCheck(newService, values)}
        />
      )}

      {cleaning && (
        <TouchConfirm
          open
          onOpenChange={(next) => {
            if (!next) setCleaning(null)
          }}
          title={t("cleaningTitle", { table: cleaning.name })}
          body={t("cleaningBody")}
          confirmLabel={t("cleaningConfirm")}
          cancelLabel={t("cleaningCancel")}
          closeLabel={t("cart.close")}
          pending={clean.isPending}
          onConfirm={async () => {
            try {
              await clean.mutateAsync(cleaning.id)
              const picked = cleaning
              setCleaning(null)
              onPick({ ...picked, status: "empty" })
            } catch (err) {
              toast.error(describeOrderError(err).message || t("cleaningFailed"))
            }
          }}
        />
      )}
    </div>
  )
}
