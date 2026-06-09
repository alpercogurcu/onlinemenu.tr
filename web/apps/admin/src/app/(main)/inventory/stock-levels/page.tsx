"use client"

import { Package } from "lucide-react"

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
import { useInventoryLevels } from "@/hooks/use-inventory"

function stockBadgeClass(qty: number): string {
  if (qty <= 0) return "bg-red-100 text-red-700 border-red-200"
  if (qty <= 10) return "bg-yellow-100 text-yellow-700 border-yellow-200"
  return "bg-green-100 text-green-700 border-green-200"
}

function stockLabel(qty: number): string {
  if (qty <= 0) return "Tükendi"
  if (qty <= 10) return "Kritik"
  return "Yeterli"
}

export default function StockLevelsPage() {
  const { data, isLoading } = useInventoryLevels()

  const levels = data ?? []
  const criticalCount = levels.filter((l) => l.quantity <= 10).length

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Stok Seviyeleri</h1>
          <p className="text-muted-foreground">Ürün stok durumlarını takip edin.</p>
        </div>
        {criticalCount > 0 && (
          <Badge variant="outline" className="bg-red-100 text-red-700 border-red-200">
            {criticalCount} kritik ürün
          </Badge>
        )}
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Stok Durumu</CardTitle>
          <CardDescription>Tüm depolardaki mevcut stok miktarları.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : levels.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Package className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Stok verisi bulunamadı</h3>
              <p className="text-sm text-muted-foreground mt-1">
                Ürün eklendiğinde stok seviyeleri burada görünür.
              </p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Ürün ID</TableHead>
                  <TableHead>Şube ID</TableHead>
                  <TableHead>Miktar</TableHead>
                  <TableHead>Durum</TableHead>
                  <TableHead>Güncelleme</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {levels.map((level) => (
                  <TableRow key={level.id}>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {level.product_id.slice(0, 8)}…
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {level.branch_id.slice(0, 8)}…
                    </TableCell>
                    <TableCell className="font-semibold">{level.quantity}</TableCell>
                    <TableCell>
                      <Badge
                        variant="outline"
                        className={stockBadgeClass(level.quantity)}
                      >
                        {stockLabel(level.quantity)}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {new Date(level.updated_at).toLocaleDateString("tr-TR")}
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
