"use client"

import { useTranslations } from "next-intl"

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { formatKurus } from "@/lib/money"
import type { SaleDetailsTaxRate } from "@/types"

// rate_bps is basis points (1/100 of a percent), e.g. 1000 bps = %10 KDV —
// hence /100 (not /10000) to get the percentage figure to display.
function formatRate(rateBps: number): string {
  return `%${(rateBps / 100).toLocaleString("tr-TR")}`
}

export function TaxTable({ rows }: { rows: SaleDetailsTaxRate[] }) {
  const t = useTranslations("dashboard.tax")

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{t("rate")}</TableHead>
          <TableHead className="text-right">{t("base")}</TableHead>
          <TableHead className="text-right">{t("tax")}</TableHead>
          <TableHead className="text-right">{t("gross")}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={row.rate_bps}>
            <TableCell>{formatRate(row.rate_bps)}</TableCell>
            <TableCell className="text-right">{formatKurus(row.base)}</TableCell>
            <TableCell className="text-right">{formatKurus(row.tax)}</TableCell>
            <TableCell className="text-right">{formatKurus(row.gross)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
