"use client"

import { Boxes, Plus } from "lucide-react"
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
import { useCreateStockItem, useStockItems } from "@/hooks/use-inventory"
import type { StockItemKind } from "@/types"

// No canonical unit enum exists on the backend (free string); this list
// covers the common units used across the platform's stock items.
const UNIT_OPTIONS = ["kg", "g", "L", "ml", "adet", "paket", "koli"]

const KIND_LABELS: Record<StockItemKind, string> = {
  raw: "Hammadde",
  intermediate: "Yarı Mamül",
  packaging: "Ambalaj",
  finished: "Mamül",
}

const KIND_BADGE_CLASS: Record<StockItemKind, string> = {
  raw: "bg-amber-100 text-amber-700 border-amber-200",
  intermediate: "bg-blue-100 text-blue-700 border-blue-200",
  packaging: "bg-purple-100 text-purple-700 border-purple-200",
  finished: "bg-green-100 text-green-700 border-green-200",
}

interface StockItemFormState {
  sku: string
  name: string
  kind: StockItemKind
  canonical_unit: string
  category: string
}

const defaultForm: StockItemFormState = {
  sku: "",
  name: "",
  kind: "raw",
  canonical_unit: "kg",
  category: "",
}

export default function StockItemsPage() {
  const [sheetOpen, setSheetOpen] = useState(false)
  const [form, setForm] = useState<StockItemFormState>(defaultForm)

  const { data, isLoading } = useStockItems()
  const createStockItem = useCreateStockItem()

  const handleOpen = () => {
    setForm(defaultForm)
    setSheetOpen(true)
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.sku.trim() || !form.name.trim()) {
      toast.error("SKU ve ad alanları zorunludur")
      return
    }
    try {
      await createStockItem.mutateAsync({
        sku: form.sku.trim(),
        name: form.name.trim(),
        kind: form.kind,
        canonical_unit: form.canonical_unit,
        category: form.category.trim() || undefined,
        is_active: true,
      })
      toast.success("Stok kalemi eklendi")
      setSheetOpen(false)
    } catch {
      toast.error("Stok kalemi eklenemedi")
    }
  }

  const items = data ?? []

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Stok Kalemleri</h1>
          <p className="text-muted-foreground">
            Hammadde, yarı mamül, ambalaj ve mamül stok kalemlerini yönetin.
          </p>
        </div>
        <Button onClick={handleOpen}>
          <Plus className="size-4" />
          Stok Kalemi Ekle
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Stok Kalemi Listesi</CardTitle>
          <CardDescription>Toplam {items.length} stok kalemi.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : items.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Boxes className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Henüz stok kalemi eklenmedi</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">
                İlk stok kaleminizi ekleyerek başlayın.
              </p>
              <Button onClick={handleOpen}>
                <Plus className="size-4" />
                İlk stok kalemini ekle
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>SKU</TableHead>
                  <TableHead>Ad</TableHead>
                  <TableHead>Tip</TableHead>
                  <TableHead>Birim</TableHead>
                  <TableHead>Kategori</TableHead>
                  <TableHead>Durum</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {item.sku}
                    </TableCell>
                    <TableCell className="font-medium">{item.name}</TableCell>
                    <TableCell>
                      <Badge variant="outline" className={KIND_BADGE_CLASS[item.kind]}>
                        {KIND_LABELS[item.kind]}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{item.canonical_unit}</TableCell>
                    <TableCell className="text-muted-foreground">{item.category || "—"}</TableCell>
                    <TableCell>
                      <Badge
                        variant="outline"
                        className={
                          item.is_active
                            ? "bg-green-100 text-green-700 border-green-200"
                            : "bg-gray-100 text-gray-600 border-gray-200"
                        }
                      >
                        {item.is_active ? "Aktif" : "Pasif"}
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
            <SheetTitle>Yeni Stok Kalemi</SheetTitle>
            <SheetDescription>
              Hammadde, yarı mamül, ambalaj veya mamül stok kalemi ekleyin.
            </SheetDescription>
          </SheetHeader>
          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="stock-item-sku">SKU</Label>
              <Input
                id="stock-item-sku"
                placeholder="örn: UN-001"
                value={form.sku}
                onChange={(e) => setForm((f) => ({ ...f, sku: e.target.value }))}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="stock-item-name">Ad</Label>
              <Input
                id="stock-item-name"
                placeholder="örn: Un (Tip 550)"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="stock-item-kind">Tip</Label>
              <Select
                id="stock-item-kind"
                value={form.kind}
                onChange={(e) => setForm((f) => ({ ...f, kind: e.target.value as StockItemKind }))}
              >
                <SelectItem value="raw">Hammadde</SelectItem>
                <SelectItem value="intermediate">Yarı Mamül</SelectItem>
                <SelectItem value="packaging">Ambalaj</SelectItem>
                <SelectItem value="finished">Mamül</SelectItem>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="stock-item-unit">Birim</Label>
              <Select
                id="stock-item-unit"
                value={form.canonical_unit}
                onChange={(e) => setForm((f) => ({ ...f, canonical_unit: e.target.value }))}
              >
                {UNIT_OPTIONS.map((unit) => (
                  <SelectItem key={unit} value={unit}>
                    {unit}
                  </SelectItem>
                ))}
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="stock-item-category">Kategori (opsiyonel)</Label>
              <Input
                id="stock-item-category"
                placeholder="örn: Kuru Gıda"
                value={form.category}
                onChange={(e) => setForm((f) => ({ ...f, category: e.target.value }))}
              />
            </div>
            <Button type="submit" className="w-full" disabled={createStockItem.isPending}>
              {createStockItem.isPending ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </form>
        </SheetContent>
      </Sheet>
    </div>
  )
}
