"use client"

import { BarChart3 } from "lucide-react"
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
import { useStockMovements, useWarehouses } from "@/hooks/use-inventory"
import type { MovementType } from "@/types"

const INBOUND_TYPES: MovementType[] = ["in", "reserve"]

function movementTypeBadge(type: MovementType): string {
  switch (type) {
    case "in":
      return "bg-status-success-bg text-status-success-fg border-status-success-border"
    case "out":
      return "bg-status-danger-bg text-status-danger-fg border-status-danger-border"
    case "adjust":
      return "bg-status-info-bg text-status-info-fg border-status-info-border"
    case "transfer":
      return "bg-status-info-bg text-status-info-fg border-status-info-border"
    case "reserve":
      return "bg-status-warning-bg text-status-warning-fg border-status-warning-border"
    case "release":
      return "bg-status-neutral-bg text-status-neutral-fg border-status-neutral-border"
    default:
      return "bg-status-neutral-bg text-status-neutral-fg border-status-neutral-border"
  }
}

function movementTypeLabel(type: MovementType): string {
  const labels: Record<MovementType, string> = {
    in: "Giriş",
    out: "Çıkış",
    adjust: "Düzeltme",
    transfer: "Transfer",
    reserve: "Rezervasyon",
    release: "Serbest Bırakma",
  }
  return labels[type] ?? type
}

export default function MovementsPage() {
  const { data: warehouses, isLoading: warehousesLoading } = useWarehouses()
  const [warehouseId, setWarehouseId] = useState<string>("")

  useEffect(() => {
    if (!warehouseId && warehouses && warehouses.length > 0) {
      setWarehouseId(warehouses[0].id)
    }
  }, [warehouseId, warehouses])

  const { data, isLoading } = useStockMovements({
    warehouse_id: warehouseId || undefined,
    limit: 50,
  })

  const movements = data ?? []

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Stok Hareketleri</h1>
        <p className="text-muted-foreground">Depo bazlı stok giriş ve çıkışlarını görüntüleyin.</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Depo Seçimi</CardTitle>
          <CardDescription>Hareketlerini görmek istediğiniz depoyu seçin.</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="max-w-sm space-y-2">
            <Label htmlFor="movement-warehouse-select">Depo</Label>
            {warehousesLoading ? (
              <Skeleton className="h-9 w-full" />
            ) : (
              <Select
                id="movement-warehouse-select"
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
          <CardTitle>Hareket Geçmişi</CardTitle>
          <CardDescription>Son 50 stok hareketi.</CardDescription>
        </CardHeader>
        <CardContent>
          {!warehouseId ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <BarChart3 className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Depo seçilmedi</h3>
              <p className="text-sm text-muted-foreground mt-1">
                Hareketleri görmek için yukarıdan bir depo seçin.
              </p>
            </div>
          ) : isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : movements.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <BarChart3 className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Hareket bulunamadı</h3>
              <p className="text-sm text-muted-foreground mt-1">
                Bu depoda stok işlemi gerçekleştikçe burada görünür.
              </p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Stok Kalemi ID</TableHead>
                  <TableHead>Tip</TableHead>
                  <TableHead>Miktar</TableHead>
                  <TableHead>Referans</TableHead>
                  <TableHead>Tarih</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {movements.map((mv) => {
                  const isInbound = INBOUND_TYPES.includes(mv.movement_type)
                  const signedQuantity =
                    mv.movement_type === "adjust"
                      ? mv.quantity
                      : isInbound
                        ? mv.quantity
                        : -mv.quantity
                  return (
                    <TableRow key={mv.id}>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {mv.stock_item_id.slice(0, 8)}…
                      </TableCell>
                      <TableCell>
                        <Badge variant="outline" className={movementTypeBadge(mv.movement_type)}>
                          {movementTypeLabel(mv.movement_type)}
                        </Badge>
                      </TableCell>
                      <TableCell
                        className={
                          signedQuantity >= 0
                            ? "text-status-success-fg font-medium"
                            : "text-status-danger-fg font-medium"
                        }
                      >
                        {signedQuantity >= 0 ? "+" : ""}
                        {signedQuantity}
                      </TableCell>
                      <TableCell className="text-muted-foreground text-xs">
                        {mv.reference_id ? mv.reference_id.slice(0, 8) + "…" : "—"}
                      </TableCell>
                      <TableCell className="text-muted-foreground text-sm">
                        {new Date(mv.created_at).toLocaleString("tr-TR")}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
