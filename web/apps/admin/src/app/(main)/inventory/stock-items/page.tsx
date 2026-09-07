"use client"

import { Boxes, Lock, Plus } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import { FormDialog } from "@/components/layouts/form-dialog"
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
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { isRestrictedStockItem, useCreateStockItem, useStockItems } from "@/hooks/use-inventory"
import type { StockItem, StockItemKind, SupplyMode } from "@/types"

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
  raw: "bg-status-warning-bg text-status-warning-fg border-status-warning-border",
  intermediate: "bg-status-info-bg text-status-info-fg border-status-info-border",
  packaging: "bg-status-info-bg text-status-info-fg border-status-info-border",
  finished: "bg-status-success-bg text-status-success-fg border-status-success-border",
}

// ADR-DATA-007: supply_mode is only resolved (and only meaningful) on the
// list endpoint. A restricted row has no supply_mode key at all — it IS an
// exclusive_hq item (that is why the caller only sees the BTO-catalog shape).
const SUPPLY_MODE_LABELS: Record<SupplyMode, string> = {
  exclusive_hq: "Merkez",
  approved_suppliers: "Onaylı tedarikçi",
  free: "Serbest",
}

const SUPPLY_MODE_BADGE_CLASS: Record<SupplyMode, string> = {
  exclusive_hq: "bg-status-neutral-bg text-status-neutral-fg border-status-neutral-border",
  approved_suppliers: "bg-status-info-bg text-status-info-fg border-status-info-border",
  free: "bg-status-success-bg text-status-success-fg border-status-success-border",
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
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState<StockItemFormState>(defaultForm)

  const { data, isLoading } = useStockItems()
  const createStockItem = useCreateStockItem()

  const handleOpen = () => {
    setForm(defaultForm)
    setDialogOpen(true)
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
      setDialogOpen(false)
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
                  <TableHead>Tedarik</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((item) => {
                  // `full` is null for a restricted (exclusive_hq) row: the
                  // backend omits kind/category/is_active/supply_mode
                  // entirely for those. isRestrictedStockItem only returns a
                  // boolean here (not a narrowed type) because StockItem is
                  // structurally a superset of RestrictedStockItem — TS's
                  // type-guard exclusion would otherwise narrow the "full"
                  // branch to `never`; the cast is safe given the boolean.
                  const restricted = isRestrictedStockItem(item)
                  const full = restricted ? null : (item as StockItem)
                  // A restricted row structurally cannot be anything but
                  // exclusive_hq (that's exactly why the branch principal
                  // only sees the BTO-catalog projection for it).
                  const supplyMode: SupplyMode = full
                    ? (full.supply_mode ?? "exclusive_hq")
                    : "exclusive_hq"
                  return (
                    <TableRow key={item.id}>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {item.sku}
                      </TableCell>
                      <TableCell className="font-medium">{item.name}</TableCell>
                      <TableCell>
                        {full ? (
                          <Badge variant="outline" className={KIND_BADGE_CLASS[full.kind]}>
                            {KIND_LABELS[full.kind]}
                          </Badge>
                        ) : (
                          <span className="text-muted-foreground text-sm">—</span>
                        )}
                      </TableCell>
                      <TableCell className="text-muted-foreground">{item.canonical_unit}</TableCell>
                      <TableCell className="text-muted-foreground">
                        {full ? full.category || "—" : "—"}
                      </TableCell>
                      <TableCell>
                        {full ? (
                          <Badge
                            variant="outline"
                            className={
                              full.is_active
                                ? "bg-status-success-bg text-status-success-fg border-status-success-border"
                                : "bg-status-neutral-bg text-status-neutral-fg border-status-neutral-border"
                            }
                          >
                            {full.is_active ? "Aktif" : "Pasif"}
                          </Badge>
                        ) : (
                          <span className="text-muted-foreground text-sm">—</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <Badge
                          variant="outline"
                          className={`gap-1 ${SUPPLY_MODE_BADGE_CLASS[supplyMode]}`}
                        >
                          {supplyMode === "exclusive_hq" && <Lock className="size-3" />}
                          {SUPPLY_MODE_LABELS[supplyMode]}
                        </Badge>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <FormDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        title="Yeni Stok Kalemi"
        description="Hammadde, yarı mamül, ambalaj veya mamül stok kalemi ekleyin."
        busy={createStockItem.isPending}
        footer={
          <>
            <Button type="button" variant="outline" onClick={() => setDialogOpen(false)}>
              Vazgeç
            </Button>
            <Button type="submit" form="stock-item-form" disabled={createStockItem.isPending}>
              {createStockItem.isPending ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </>
        }
      >
        <form id="stock-item-form" onSubmit={handleSubmit} className="space-y-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
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
          </div>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
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
        </form>
      </FormDialog>
    </div>
  )
}
