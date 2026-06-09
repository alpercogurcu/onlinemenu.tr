"use client"

import { BarChart3 } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useInventoryTransactions } from "@/hooks/use-inventory"

function transactionTypeBadge(type: string): string {
  switch (type) {
    case "in":
      return "bg-green-100 text-green-700 border-green-200"
    case "out":
      return "bg-red-100 text-red-700 border-red-200"
    case "adjustment":
      return "bg-blue-100 text-blue-700 border-blue-200"
    default:
      return "bg-gray-100 text-gray-600 border-gray-200"
  }
}

function transactionTypeLabel(type: string): string {
  const labels: Record<string, string> = {
    in: "Giriş",
    out: "Çıkış",
    adjustment: "Düzeltme",
    transfer: "Transfer",
  }
  return labels[type] ?? type
}

export default function MovementsPage() {
  const { data, isLoading } = useInventoryTransactions()

  const transactions = data ?? []

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Stok Hareketleri</h1>
        <p className="text-muted-foreground">Tüm stok giriş ve çıkışlarını görüntüleyin.</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Hareket Geçmişi</CardTitle>
          <CardDescription>Son 50 stok hareketi.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : transactions.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <BarChart3 className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Hareket bulunamadı</h3>
              <p className="text-sm text-muted-foreground mt-1">
                Stok işlemleri gerçekleştikçe burada görünür.
              </p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Ürün ID</TableHead>
                  <TableHead>Şube</TableHead>
                  <TableHead>Tip</TableHead>
                  <TableHead>Miktar</TableHead>
                  <TableHead>Referans</TableHead>
                  <TableHead>Tarih</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {transactions.map((tx) => (
                  <TableRow key={tx.id}>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {tx.product_id.slice(0, 8)}…
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {tx.branch_id.slice(0, 8)}…
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline" className={transactionTypeBadge(tx.type)}>
                        {transactionTypeLabel(tx.type)}
                      </Badge>
                    </TableCell>
                    <TableCell className={tx.quantity_delta >= 0 ? "text-green-600 font-medium" : "text-red-600 font-medium"}>
                      {tx.quantity_delta >= 0 ? "+" : ""}{tx.quantity_delta}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-xs">
                      {tx.reference_id ? tx.reference_id.slice(0, 8) + "…" : "—"}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {new Date(tx.created_at).toLocaleString("tr-TR")}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
