"use client"

import { useTranslations } from "next-intl"

import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { formatKurus } from "@/lib/money"
import type { SaleDetailsCashSession } from "@/types"

// difference is null when the session is still open (or was closed without a
// counted amount) — rendered as an em dash rather than a badge, since there
// is nothing to compare yet. Once present, 0 reads as balanced (neutral
// badge) and anything else as a discrepancy (destructive badge) regardless of
// sign — being short and being over both need the cashier's attention.
function DifferenceBadge({ amount }: { amount: number | null }) {
  if (amount === null) return <span className="text-muted-foreground">—</span>
  return (
    <Badge
      variant={amount === 0 ? "secondary" : "destructive"}
      className={amount === 0 ? "border-transparent bg-green-100 text-green-700" : undefined}
    >
      {formatKurus(amount)}
    </Badge>
  )
}

export function CashSessionsCard({ sessions }: { sessions: SaleDetailsCashSession[] }) {
  const t = useTranslations("dashboard.cashSessions")

  if (sessions.length === 0) return null

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("title")}</CardTitle>
      </CardHeader>
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("opened")}</TableHead>
              <TableHead className="text-right">{t("openingAmount")}</TableHead>
              <TableHead className="text-right">{t("cashTaken")}</TableHead>
              <TableHead className="text-right">{t("movementsNet")}</TableHead>
              <TableHead className="text-right">{t("expected")}</TableHead>
              <TableHead className="text-right">{t("counted")}</TableHead>
              <TableHead className="text-right">{t("difference")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sessions.map((session) => (
              <TableRow key={session.id}>
                <TableCell>{new Date(session.opened_at).toLocaleString("tr-TR")}</TableCell>
                <TableCell className="text-right">{formatKurus(session.opening_counted_amount)}</TableCell>
                <TableCell className="text-right">{formatKurus(session.cash_payments_taken)}</TableCell>
                <TableCell className="text-right">{formatKurus(session.movements_net)}</TableCell>
                <TableCell className="text-right">{formatKurus(session.expected_close)}</TableCell>
                <TableCell className="text-right">{formatKurus(session.closing_counted_amount)}</TableCell>
                <TableCell className="text-right">
                  <DifferenceBadge amount={session.difference} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}
