"use client"

import { Plus, Warehouse as WarehouseIcon } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectItem } from "@/components/ui/select"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useCreateWarehouse, useWarehouses } from "@/hooks/use-inventory"
import { useBranches } from "@/hooks/use-tenant"
import { useAuthStore } from "@/store/auth-store"
import type { WarehouseType } from "@/types"

interface WarehouseFormState {
  branch_id: string
  name: string
  warehouse_type: WarehouseType
}

const defaultForm: WarehouseFormState = {
  branch_id: "",
  name: "",
  warehouse_type: "depo",
}

export default function WarehousesPage() {
  const [sheetOpen, setSheetOpen] = useState(false)
  const [form, setForm] = useState<WarehouseFormState>(defaultForm)

  const tenantId = useAuthStore((s) => s.tenantId) ?? ""
  const { data: branches } = useBranches(tenantId)
  const { data, isLoading } = useWarehouses()
  const createWarehouse = useCreateWarehouse()

  const branchMap = new Map((branches ?? []).map((b) => [b.id, b.name]))

  const handleOpen = () => {
    setForm({ ...defaultForm, branch_id: branches?.[0]?.id ?? "" })
    setSheetOpen(true)
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim() || !form.branch_id) {
      toast.error("Şube ve depo adı zorunludur")
      return
    }
    try {
      await createWarehouse.mutateAsync({
        branch_id: form.branch_id,
        name: form.name.trim(),
        warehouse_type: form.warehouse_type,
        is_active: true,
      })
      toast.success("Depo eklendi")
      setSheetOpen(false)
    } catch {
      toast.error("Depo eklenemedi")
    }
  }

  const warehouses = data ?? []

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Depolar</h1>
          <p className="text-muted-foreground">İşletmenizin depo ve imalat noktalarını yönetin.</p>
        </div>
        <Button onClick={handleOpen}>
          <Plus className="size-4" />
          Depo Ekle
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Depo Listesi</CardTitle>
          <CardDescription>Toplam {warehouses.length} depo.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : warehouses.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <WarehouseIcon className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Henüz depo eklenmedi</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">
                İlk deponuzu ekleyerek başlayın.
              </p>
              <Button onClick={handleOpen}>
                <Plus className="size-4" />
                İlk depoyu ekle
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Ad</TableHead>
                  <TableHead>Şube</TableHead>
                  <TableHead>Tip</TableHead>
                  <TableHead>Durum</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {warehouses.map((wh) => (
                  <TableRow key={wh.id}>
                    <TableCell className="font-medium">{wh.name}</TableCell>
                    <TableCell className="text-muted-foreground">
                      {branchMap.get(wh.branch_id) ?? "—"}
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {wh.warehouse_type === "depo" ? "Depo" : "İmalat"}
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant="outline"
                        className={
                          wh.is_active
                            ? "bg-green-100 text-green-700 border-green-200"
                            : "bg-gray-100 text-gray-600 border-gray-200"
                        }
                      >
                        {wh.is_active ? "Aktif" : "Pasif"}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
        <SheetContent>
          <SheetHeader>
            <SheetTitle>Yeni Depo</SheetTitle>
            <SheetDescription>İşletmenize yeni bir depo veya imalat noktası ekleyin.</SheetDescription>
          </SheetHeader>
          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="warehouse-branch">Şube</Label>
              <Select
                id="warehouse-branch"
                value={form.branch_id}
                onChange={(e) => setForm((f) => ({ ...f, branch_id: e.target.value }))}
              >
                <SelectItem value="">Şube seçin</SelectItem>
                {(branches ?? []).map((b) => (
                  <SelectItem key={b.id} value={b.id}>
                    {b.name}
                  </SelectItem>
                ))}
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="warehouse-name">Ad</Label>
              <Input
                id="warehouse-name"
                placeholder="örn: Merkez Depo"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="warehouse-type">Tip</Label>
              <Select
                id="warehouse-type"
                value={form.warehouse_type}
                onChange={(e) =>
                  setForm((f) => ({ ...f, warehouse_type: e.target.value as WarehouseType }))
                }
              >
                <SelectItem value="depo">Depo</SelectItem>
                <SelectItem value="imalat">İmalat</SelectItem>
              </Select>
            </div>
            <Button type="submit" className="w-full" disabled={createWarehouse.isPending}>
              {createWarehouse.isPending ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </form>
        </SheetContent>
      </Sheet>
    </div>
  )
}
