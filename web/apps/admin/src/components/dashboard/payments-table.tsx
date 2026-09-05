"use client"

import { useTranslations } from "next-intl"

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { formatKurus } from "@/lib/money"
import type { ReportPaymentMethod, SaleDetailsPayment } from "@/types"

// Message-key suffixes for each backend method, matched 1:1 to
// dashboard.payments.method in tr.json. Kept separate from the wire value
// (snake_case, e.g. "meal_card") because next-intl keys are looked up by
// property access, not string concatenation of arbitrary characters.
const METHOD_LABEL_KEY: Record<ReportPaymentMethod, string> = {
  cash: "cash",
  terminal: "terminal",
  meal_card: "mealCard",
  comp: "comp",
  no_charge: "noCharge",
  open_account: "openAccount",
}

export function PaymentsTable({ payments }: { payments: SaleDetailsPayment[] }) {
  const t = useTranslations("dashboard.payments")
  const completed = payments.filter((p) => p.status === "completed")
  const voided = payments.filter((p) => p.status === "voided")

  function methodLabel(method: ReportPaymentMethod): string {
    return t(`method.${METHOD_LABEL_KEY[method]}`)
  }

  function rows(list: SaleDetailsPayment[]) {
    return list.map((p) => (
      <TableRow key={`${p.method}-${p.status}`}>
        <TableCell>{methodLabel(p.method)}</TableCell>
        <TableCell className="text-right">{p.count}</TableCell>
        <TableCell className="text-right">{formatKurus(p.total)}</TableCell>
      </TableRow>
    ))
  }

  return (
    <div className="space-y-4">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("method.label")}</TableHead>
            <TableHead className="text-right">{t("count")}</TableHead>
            <TableHead className="text-right">{t("total")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>{rows(completed)}</TableBody>
      </Table>

      {voided.length > 0 && (
        <div className="space-y-2">
          <p className="text-sm font-medium text-muted-foreground">{t("voidedTitle")}</p>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("method.label")}</TableHead>
                <TableHead className="text-right">{t("count")}</TableHead>
                <TableHead className="text-right">{t("total")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>{rows(voided)}</TableBody>
          </Table>
        </div>
      )}
    </div>
  )
}
