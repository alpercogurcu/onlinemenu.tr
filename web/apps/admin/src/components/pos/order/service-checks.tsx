"use client"

import { ChevronRight, Plus } from "lucide-react"
import { useTranslations } from "next-intl"
import { useState } from "react"

import { formatMoney } from "@onlinemenu/pos-core"

import { TouchSheet } from "@/components/pos/order/touch-sheet"
import { Input } from "@/components/ui/input"
import { useCheckItemCounts } from "@/hooks/use-pos-order"
import type { Check } from "@/types"

export type ServiceKind = "takeaway" | "delivery"

interface ServiceCheckListProps {
  serviceType: ServiceKind
  checks: Check[]
  onPick: (check: Check) => void
  onNew: () => void
}

/**
 * The gel al / paket side of the picker: these checks have no table tile, so
 * they are rows under the customer's name — "Alper Vural — 5 ürün · ₺940".
 * The item count arrives per check (useCheckItemCounts); until it does the
 * row shows the total alone rather than waiting.
 */
export function ServiceCheckList({ serviceType, checks, onPick, onNew }: ServiceCheckListProps) {
  const t = useTranslations("posOrder.service")
  const counts = useCheckItemCounts(checks.map((c) => c.id))
  const serviceLabel = t(serviceType)

  return (
    <div className="space-y-3">
      <button
        type="button"
        onClick={onNew}
        className="bg-primary text-primary-foreground flex min-h-14 w-full items-center justify-center gap-2 rounded-2xl text-base font-bold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 active:scale-[0.99] motion-reduce:active:scale-100"
      >
        <Plus className="size-5" aria-hidden="true" />
        {t(serviceType === "takeaway" ? "newTakeaway" : "newDelivery")}
      </button>

      {checks.length === 0 ? (
        <p className="py-10 text-center text-base text-muted-foreground">
          {t(serviceType === "takeaway" ? "emptyTakeaway" : "emptyDelivery")}
        </p>
      ) : (
        <ul className="divide-y rounded-2xl border-2 bg-card" aria-label={serviceLabel}>
          {checks.map((check) => {
            const name = check.customer_name || check.table_label
            const count = counts.get(check.id)
            const parts = [
              ...(count !== undefined && count > 0 ? [t("itemCount", { count })] : []),
              ...(check.total !== undefined ? [formatMoney(check.total)] : []),
            ]
            return (
              <li key={check.id}>
                <button
                  type="button"
                  data-testid="service-check-row"
                  onClick={() => onPick(check)}
                  aria-label={t("rowAria", { name, service: serviceLabel })}
                  className="flex min-h-16 w-full items-center gap-3 px-4 py-2 text-left outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
                >
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-lg font-bold">{name}</span>
                    {(parts.length > 0 || check.customer_phone) && (
                      <span className="block truncate text-sm text-muted-foreground tabular-nums">
                        {parts.join(" · ")}
                        {check.customer_phone ? `${parts.length > 0 ? " · " : ""}${check.customer_phone}` : ""}
                      </span>
                    )}
                  </span>
                  <ChevronRight className="size-5 shrink-0 text-muted-foreground" aria-hidden="true" />
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

export interface ServiceCheckFormValues {
  customer_name: string
  customer_phone: string
  customer_address: string
}

interface NewServiceCheckSheetProps {
  serviceType: ServiceKind
  pending: boolean
  error: string | null
  onOpenChange: (open: boolean) => void
  onSubmit: (values: ServiceCheckFormValues) => void
}

/**
 * Short form before a tableless check: gel al wants a name (phone optional),
 * paket wants name + phone (address optional — the courier may know the
 * regular). Validation is inline and in Turkish; the sheet stays open until
 * the check is actually open so a failed POST never loses the typed values.
 */
export function NewServiceCheckSheet({ serviceType, pending, error, onOpenChange, onSubmit }: NewServiceCheckSheetProps) {
  const t = useTranslations("posOrder.service")
  const [name, setName] = useState("")
  const [phone, setPhone] = useState("")
  const [address, setAddress] = useState("")
  const [touched, setTouched] = useState(false)

  const delivery = serviceType === "delivery"
  const nameMissing = name.trim() === ""
  const phoneMissing = delivery && phone.trim() === ""

  function submit() {
    setTouched(true)
    if (nameMissing || phoneMissing) return
    onSubmit({ customer_name: name.trim(), customer_phone: phone.trim(), customer_address: address.trim() })
  }

  const fieldClass = "h-14 text-base"

  return (
    <TouchSheet
      open
      onOpenChange={(next) => {
        if (!pending) onOpenChange(next)
      }}
      title={t(delivery ? "formTitleDelivery" : "formTitleTakeaway")}
      closeLabel={t("close")}
      footer={
        <div className="space-y-2">
          {error && (
            <p role="alert" className="text-status-danger-fg text-sm font-medium">
              {error}
            </p>
          )}
          <button
            type="submit"
            form="service-check-form"
            disabled={pending}
            aria-busy={pending}
            className="bg-primary text-primary-foreground min-h-14 w-full rounded-xl text-base font-bold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50"
          >
            {pending ? t("starting") : t("start")}
          </button>
        </div>
      }
    >
      <form
        id="service-check-form"
        className="space-y-4 p-4"
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
      >
        <div className="space-y-1.5">
          <label htmlFor="service-customer-name" className="text-base font-medium">
            {t("name")}
          </label>
          <Input
            id="service-customer-name"
            className={fieldClass}
            value={name}
            autoFocus
            autoComplete="off"
            onChange={(e) => setName(e.target.value)}
            aria-invalid={touched && nameMissing}
          />
          {touched && nameMissing && (
            <p role="alert" className="text-status-danger-fg text-sm font-medium">
              {t("nameRequired")}
            </p>
          )}
        </div>

        <div className="space-y-1.5">
          <label htmlFor="service-customer-phone" className="text-base font-medium">
            {delivery ? t("phone") : t("phoneOptional")}
          </label>
          <Input
            id="service-customer-phone"
            className={fieldClass}
            value={phone}
            type="tel"
            inputMode="tel"
            autoComplete="off"
            onChange={(e) => setPhone(e.target.value)}
            aria-invalid={touched && phoneMissing}
          />
          {touched && phoneMissing && (
            <p role="alert" className="text-status-danger-fg text-sm font-medium">
              {t("phoneRequired")}
            </p>
          )}
        </div>

        {delivery && (
          <div className="space-y-1.5">
            <label htmlFor="service-customer-address" className="text-base font-medium">
              {t("address")}
            </label>
            <textarea
              id="service-customer-address"
              rows={3}
              value={address}
              onChange={(e) => setAddress(e.target.value)}
              className="border-input bg-transparent focus-visible:ring-ring/50 w-full rounded-md border px-3 py-2 text-base shadow-xs outline-none focus-visible:ring-[3px]"
            />
          </div>
        )}
      </form>
    </TouchSheet>
  )
}
