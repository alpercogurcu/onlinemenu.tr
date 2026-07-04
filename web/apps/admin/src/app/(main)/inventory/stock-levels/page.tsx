"use client"

import { Package } from "lucide-react"
import { useEffect, useState } from "react"

import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Label } from "@/components/ui/label"
import { Select, SelectItem } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useStockLevels, useWarehouses } from "@/hooks/use-inventory"
import type { InventoryLevel } from "@/types"

function stockBadgeClass(level: InventoryLevel): string {
  const threshold = level.reorder_point ?? 10
  if (level.available <= 0) return "bg-red-100 text-red-700 border-red-200"
  if (level.available <= threshold) return "bg-yellow-100 text-yellow-700 border-yellow-200"
  return "bg-green-100 text-green-700 border-green-200"
}

function stockLabel(level: InventoryLevel): string {
  const threshold = level.reorder_point ?? 10
  if (level.available <= 0) return "Tükendi"
  if (level.available <= threshold) return "Kritik"
  return "Yeterli"
}

export default function StockLevelsPage() {
  const { data: warehouses, isLoading: warehousesLoading } = useWarehouses()
  const [warehouseId, setWarehouseId] = useState<string>("")

  useEffect(() => {
    if (!warehouseId && warehouses && warehouses.length > 0) {
      setWarehouseId(warehouses[0].id)
    }
  }, [warehouseId, warehouses])

  const { data, isLoading } = useStockLevels({ warehouse_id: warehouseId || undefined })

  const levels = data ?? []
  const criticalCount = levels.filter((l) => l.available <= (l.reorder_point ?? 10)).length

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Stok Seviyeleri</h1>
          <p className="text-muted-foreground">Depo bazlı stok durumlarını takip edin.</p>
        </div>
        {criticalCount > 0 && (
          <Badge variant="outline" className="bg-red-100 text-red-700 border-red-200">
            {criticalCount} kritik ürün
          </Badge>
        )}
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Depo Seçimi</CardTitle>
          <CardDescription>Stok seviyelerini görmek istediğiniz depoyu seçin.</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="max-w-sm space-y-2">
            <Label htmlFor="warehouse-select">Depo</Label>
            {warehousesLoading ? (
              <Skeleton className="h-9 w-full" />
            ) : (
              <Select
                id="warehouse-select"
                value={warehouseId}
                onChange={(e) => setWarehouseId(e.target.value)}
              >
                <SelectItem value="">Depo seçin</SelectItem>
                {(warehouses ?? []).map((wh) => (
                  <SelectItem key={wh.id} value={wh.id}>
                    {wh.name}
                  </SelectItem>
                ))}
              </Select>
            )}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Stok Durumu</CardTitle>
          <CardDescription>Seçili depodaki mevcut stok miktarları.</CardDescription>
        </CardHeader>
        <CardContent>
          {!warehouseId ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Package className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Depo seçilmedi</h3>
              <p className="text-sm text-muted-foreground mt-1">
                Stok seviyelerini görmek için yukarıdan bir depo seçin.
              </p>
            </div>
          ) : isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : levels.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Package className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Stok verisi bulunamadı</h3>
              <p className="text-sm text-muted-foreground mt-1">
                Bu depoda stok kalemi bulunmuyor.
              </p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Stok Kalemi ID</TableHead>
                  <TableHead>Eldeki</TableHead>
                  <TableHead>Rezerve</TableHead>
                  <TableHead>Kullanılabilir</TableHead>
                  <TableHead>Birim</TableHead>
                  <TableHead>Durum</TableHead>
                  <TableHead>Güncelleme</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {levels.map((level) => (
                  <TableRow key={level.id}>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {level.stock_item_id.slice(0, 8)}…
                    </TableCell>
                    <TableCell>{level.on_hand}</TableCell>
                    <TableCell>{level.reserved}</TableCell>
                    <TableCell className="font-semibold">{level.available}</TableCell>
                    <TableCell className="text-muted-foreground">{level.unit}</TableCell>
                    <TableCell>
                      <Badge variant="outline" className={stockBadgeClass(level)}>
                        {stockLabel(level)}
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
