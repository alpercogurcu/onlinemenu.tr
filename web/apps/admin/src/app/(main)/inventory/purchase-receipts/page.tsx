"use client"

import axios from "axios"
import { Plus, Receipt, Trash2 } from "lucide-react"
import { useEffect, useState } from "react"
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
import {
  isRestrictedStockItem,
  useCreatePurchaseReceipt,
  usePurchaseReceipts,
  useStockItems,
  useWarehouses,
} from "@/hooks/use-inventory"
import { useParties } from "@/hooks/use-parties"
import type { StockItem } from "@/types"

// FREE_SUPPLIER_VALUE is a sentinel option value (not a real party id) that
// switches the supplier field from "pick a registered party" to "type a
// free-text name" (pazar/market elden fiş — ADR-DATA-007 karar 3).
const FREE_SUPPLIER_VALUE = "__free_text__"

interface ReceiptItemRow {
  stock_item_id: string
  quantity: string
  unit: string
  unit_price: string
  brand: string
}

const emptyRow: ReceiptItemRow = {
  stock_item_id: "",
  quantity: "",
  unit: "",
  unit_price: "",
  brand: "",
}

interface ReceiptFormState {
  warehouse_id: string
  supplierSelection: string
  supplier_name: string
  receipt_no: string
  receipt_date: string
  note: string
  items: ReceiptItemRow[]
}

function todayISODate(): string {
  return new Date().toISOString().slice(0, 10)
}

function defaultForm(warehouseId: string): ReceiptFormState {
  return {
    warehouse_id: warehouseId,
    supplierSelection: "",
    supplier_name: "",
    receipt_no: "",
    receipt_date: todayISODate(),
    note: "",
    items: [{ ...emptyRow }],
  }
}

// extractSupplyPolicyStockItemID parses the stock item UUID out of the
// backend's ErrSupplyPolicyViolation message ("stock item <uuid> is
// exclusive_hq: ..." / "stock item <uuid> requires an approved supplier..."
// — see purchase_receipt_service.go) so the offending line can be
// highlighted. The message is a single, whole-request 422; only ONE
// violating line is ever reported (the first one enforceSupplyPolicy
// rejects), even if several lines would fail.
function extractSupplyPolicyStockItemID(message: string): string | null {
  const match = /stock item ([0-9a-fA-F-]{36})/.exec(message)
  return match ? match[1] : null
}

function friendlyPolicyMessage(message: string, itemLabel: string): string {
  if (message.includes("exclusive_hq")) {
    return `${itemLabel}: Bu kalem yalnızca merkezden (BTO) tedarik edilebilir`
  }
  if (message.includes("approved supplier")) {
    return `${itemLabel}: Bu kalem yalnızca onaylı tedarikçilerden alınabilir`
  }
  return `${itemLabel}: ${message}`
}

export default function PurchaseReceiptsPage() {
  const { data: warehouses, isLoading: warehousesLoading } = useWarehouses()
  const [warehouseId, setWarehouseId] = useState<string>("")

  useEffect(() => {
    if (!warehouseId && warehouses && warehouses.length > 0) {
      setWarehouseId(warehouses[0].id)
    }
  }, [warehouseId, warehouses])

  const { data: receipts, isLoading } = usePurchaseReceipts({
    warehouse_id: warehouseId || undefined,
  })
  const { data: stockItems } = useStockItems()
  const { data: suppliers } = useParties({ type: "supplier" })
  const createReceipt = useCreatePurchaseReceipt()

  const [sheetOpen, setSheetOpen] = useState(false)
  const [form, setForm] = useState<ReceiptFormState>(defaultForm(""))
  const [lineErrors, setLineErrors] = useState<Record<number, string>>({})

  const supplierNames = new Map((suppliers ?? []).map((party) => [party.id, party.name]))
  const stockItemNames = new Map((stockItems ?? []).map((item) => [item.id, item.name]))
  // exclusive_hq items can never be locally receipted (backend rejects them
  // outright) — hide them from the line-item picker rather than let the
  // user hit a guaranteed 422.
  const selectableStockItems = (stockItems ?? []).filter(
    (item): item is StockItem => !isRestrictedStockItem(item),
  )

  const handleOpen = () => {
    setForm(defaultForm(warehouseId))
    setLineErrors({})
    setSheetOpen(true)
  }

  const updateRow = (index: number, patch: Partial<ReceiptItemRow>) => {
    setForm((f) => ({
      ...f,
      items: f.items.map((row, i) => (i === index ? { ...row, ...patch } : row)),
    }))
  }

  const addRow = () => {
    setForm((f) => ({ ...f, items: [...f.items, { ...emptyRow }] }))
  }

  const removeRow = (index: number) => {
    setForm((f) => ({ ...f, items: f.items.filter((_, i) => i !== index) }))
    setLineErrors({})
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setLineErrors({})

    if (!form.warehouse_id) {
      toast.error("Depo seçilmelidir")
      return
    }
    if (form.items.length === 0) {
      toast.error("En az bir satır eklenmelidir")
      return
    }
    for (const row of form.items) {
      if (!row.stock_item_id) {
        toast.error("Her satırda stok kalemi seçilmelidir")
        return
      }
      const qty = Number(row.quantity)
      if (!row.quantity || !(qty > 0)) {
        toast.error("Her satırda miktar sıfırdan büyük olmalıdır")
        return
      }
      if (!row.unit.trim()) {
        toast.error("Her satırda birim zorunludur")
        return
      }
      const price = Number(row.unit_price)
      if (row.unit_price === "" || Number.isNaN(price) || price < 0) {
        toast.error("Birim fiyat negatif olamaz")
        return
      }
    }

    const isFreeSupplier = form.supplierSelection === FREE_SUPPLIER_VALUE
    try {
      await createReceipt.mutateAsync({
        warehouse_id: form.warehouse_id,
        supplier_party_id:
          form.supplierSelection && !isFreeSupplier ? form.supplierSelection : undefined,
        supplier_name: isFreeSupplier ? form.supplier_name.trim() || undefined : undefined,
        receipt_no: form.receipt_no.trim() || undefined,
        receipt_date: form.receipt_date || undefined,
        note: form.note.trim() || undefined,
        items: form.items.map((row) => ({
          stock_item_id: row.stock_item_id,
          quantity: Number(row.quantity),
          unit: row.unit.trim(),
          unit_price: Number(row.unit_price),
          brand: row.brand.trim() || undefined,
        })),
      })
      toast.success("Fiş kaydedildi")
      setSheetOpen(false)
    } catch (err) {
      if (
        axios.isAxiosError(err) &&
        err.response?.status === 422 &&
        typeof err.response.data === "string"
      ) {
        const message = err.response.data.trim()
        const stockItemId = extractSupplyPolicyStockItemID(message)
        const rowIndex = stockItemId
          ? form.items.findIndex((row) => row.stock_item_id === stockItemId)
          : -1
        if (stockItemId && rowIndex >= 0) {
          const itemLabel = stockItemNames.get(stockItemId) ?? stockItemId
          const friendly = friendlyPolicyMessage(message, itemLabel)
          setLineErrors({ [rowIndex]: friendly })
          toast.error(friendly)
          return
        }
        toast.error(message)
        return
      }
      toast.error("Fiş kaydedilemedi")
    }
  }

  const receiptsList = receipts ?? []

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Elden Fiş / Faturasız Alım</h1>
          <p className="text-muted-foreground">
            Pazar/market gibi faturasız alımları depo bazlı fiş olarak kaydedin.
          </p>
        </div>
        <Button onClick={handleOpen} disabled={!warehouseId}>
          <Plus className="size-4" />
          Fiş Ekle
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Depo Seçimi</CardTitle>
          <CardDescription>Fişlerini görmek istediğiniz depoyu seçin.</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="max-w-sm space-y-2">
            <Label htmlFor="receipt-warehouse-select">Depo</Label>
            {warehousesLoading ? (
              <Skeleton className="h-9 w-full" />
            ) : (
              <Select
                id="receipt-warehouse-select"
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
          <CardTitle>Fiş Listesi</CardTitle>
          <CardDescription>Seçili depoya ait elden fiş kayıtları.</CardDescription>
        </CardHeader>
        <CardContent>
          {!warehouseId ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Receipt className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Depo seçilmedi</h3>
              <p className="text-sm text-muted-foreground mt-1">
                Fişleri görmek için yukarıdan bir depo seçin.
              </p>
            </div>
          ) : isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : receiptsList.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Receipt className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Henüz fiş kaydedilmedi</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">
                Bu depoya ait ilk elden fişi ekleyerek başlayın.
              </p>
              <Button onClick={handleOpen}>
                <Plus className="size-4" />
                İlk fişi ekle
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Tarih</TableHead>
                  <TableHead>Tedarikçi</TableHead>
                  <TableHead>Fiş No</TableHead>
                  <TableHead>Toplam</TableHead>
                  <TableHead>Not</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {receiptsList.map((rcpt) => (
                  <TableRow key={rcpt.id}>
                    <TableCell className="text-muted-foreground text-sm">
                      {new Date(rcpt.receipt_date).toLocaleDateString("tr-TR")}
                    </TableCell>
                    <TableCell className="font-medium">
                      {rcpt.supplier_party_id
                        ? (supplierNames.get(rcpt.supplier_party_id) ?? rcpt.supplier_party_id)
                        : rcpt.supplier_name || "—"}
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {rcpt.receipt_no || "—"}
                    </TableCell>
                    <TableCell className="font-medium">
                      {rcpt.total.toLocaleString("tr-TR", {
                        style: "currency",
                        currency: rcpt.currency || "TRY",
                      })}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {rcpt.note || "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
        <SheetContent className="sm:max-w-lg">
          <SheetHeader>
            <SheetTitle>Yeni Fiş</SheetTitle>
            <SheetDescription>
              Faturasız alım (pazar/market) için depo, tedarikçi ve satırları girin. Bir satır
              tedarik politikasına aykırıysa fiş bütün olarak reddedilir.
            </SheetDescription>
          </SheetHeader>
          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="receipt-warehouse">Depo</Label>
              <Select
                id="receipt-warehouse"
                value={form.warehouse_id}
                onChange={(e) => setForm((f) => ({ ...f, warehouse_id: e.target.value }))}
              >
                <SelectItem value="">Depo seçin</SelectItem>
                {(warehouses ?? []).map((wh) => (
                  <SelectItem key={wh.id} value={wh.id}>
                    {wh.name}
                  </SelectItem>
                ))}
              </Select>
            </div>

            <div className="space-y-2">
              <Label htmlFor="receipt-supplier">Tedarikçi</Label>
              <Select
                id="receipt-supplier"
                value={form.supplierSelection}
                onChange={(e) => setForm((f) => ({ ...f, supplierSelection: e.target.value }))}
              >
                <SelectItem value="">Tedarikçi seçin</SelectItem>
                <SelectItem value={FREE_SUPPLIER_VALUE}>Serbest / Pazar (metin gir)</SelectItem>
                {(suppliers ?? []).map((party) => (
                  <SelectItem key={party.id} value={party.id}>
                    {party.name}
                  </SelectItem>
                ))}
              </Select>
              {form.supplierSelection === FREE_SUPPLIER_VALUE && (
                <Input
                  placeholder="örn: Salı Pazarı"
                  value={form.supplier_name}
                  onChange={(e) => setForm((f) => ({ ...f, supplier_name: e.target.value }))}
                />
              )}
            </div>

            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-2">
                <Label htmlFor="receipt-date">Tarih</Label>
                <Input
                  id="receipt-date"
                  type="date"
                  value={form.receipt_date}
                  onChange={(e) => setForm((f) => ({ ...f, receipt_date: e.target.value }))}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="receipt-no">Fiş No (opsiyonel)</Label>
                <Input
                  id="receipt-no"
                  value={form.receipt_no}
                  onChange={(e) => setForm((f) => ({ ...f, receipt_no: e.target.value }))}
                />
              </div>
            </div>

            <div className="space-y-2">
              <Label htmlFor="receipt-note">Not (opsiyonel)</Label>
              <Input
                id="receipt-note"
                value={form.note}
                onChange={(e) => setForm((f) => ({ ...f, note: e.target.value }))}
              />
            </div>

            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <Label>Satırlar</Label>
                <Badge variant="outline">{form.items.length} satır</Badge>
              </div>
              {form.items.map((row, idx) => (
                <div key={idx} className="space-y-2 rounded-md border p-3">
                  <div className="flex items-center justify-between">
                    <span className="text-sm font-medium">Satır {idx + 1}</span>
                    {form.items.length > 1 && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => removeRow(idx)}
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    )}
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor={`receipt-item-stock-${idx}`}>Stok Kalemi</Label>
                    <Select
                      id={`receipt-item-stock-${idx}`}
                      value={row.stock_item_id}
                      onChange={(e) => {
                        const stockItemId = e.target.value
                        const selected = selectableStockItems.find((i) => i.id === stockItemId)
                        updateRow(idx, {
                          stock_item_id: stockItemId,
                          unit: row.unit || selected?.canonical_unit || "",
                        })
                      }}
                    >
                      <SelectItem value="">Stok kalemi seçin</SelectItem>
                      {selectableStockItems.map((item) => (
                        <SelectItem key={item.id} value={item.id}>
                          {item.name} ({item.sku})
                        </SelectItem>
                      ))}
                    </Select>
                  </div>
                  <div className="grid grid-cols-3 gap-2">
                    <div className="space-y-2">
                      <Label htmlFor={`receipt-item-qty-${idx}`}>Miktar</Label>
                      <Input
                        id={`receipt-item-qty-${idx}`}
                        type="number"
                        step="0.001"
                        min="0"
                        value={row.quantity}
                        onChange={(e) => updateRow(idx, { quantity: e.target.value })}
                      />
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor={`receipt-item-unit-${idx}`}>Birim</Label>
                      <Input
                        id={`receipt-item-unit-${idx}`}
                        value={row.unit}
                        onChange={(e) => updateRow(idx, { unit: e.target.value })}
                      />
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor={`receipt-item-price-${idx}`}>Birim Fiyat</Label>
                      <Input
                        id={`receipt-item-price-${idx}`}
                        type="number"
                        step="0.01"
                        min="0"
                        value={row.unit_price}
                        onChange={(e) => updateRow(idx, { unit_price: e.target.value })}
                      />
                    </div>
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor={`receipt-item-brand-${idx}`}>Marka (opsiyonel)</Label>
                    <Input
                      id={`receipt-item-brand-${idx}`}
                      placeholder="örn: Heinz"
                      value={row.brand}
                      onChange={(e) => updateRow(idx, { brand: e.target.value })}
                    />
                  </div>
                  {lineErrors[idx] && (
                    <p className="text-xs text-destructive">{lineErrors[idx]}</p>
                  )}
                </div>
              ))}
              <Button type="button" variant="outline" size="sm" onClick={addRow}>
                <Plus className="size-4" />
                Satır ekle
              </Button>
            </div>

            <Button type="submit" className="w-full" disabled={createReceipt.isPending}>
              {createReceipt.isPending ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </form>
        </SheetContent>
      </Sheet>
    </div>
  )
}
