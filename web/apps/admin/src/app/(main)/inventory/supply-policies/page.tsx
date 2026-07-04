"use client"

import axios from "axios"
import { Lock, Plus, ShieldCheck } from "lucide-react"
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
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
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
import { useCreateSupplyPolicy, useStockItems, useSupplyPolicies } from "@/hooks/use-inventory"
import { useParties } from "@/hooks/use-parties"
import type { SupplyMode, SupplyPolicy, SupplyScope } from "@/types"

const SCOPE_LABELS: Record<SupplyScope, string> = {
  stock_item: "Stok Kalemi",
  category: "Kategori",
  tenant_default: "Tenant Geneli",
}

const MODE_LABELS: Record<SupplyMode, string> = {
  exclusive_hq: "Yalnızca Merkezden",
  approved_suppliers: "Onaylı Tedarikçiler",
  free: "Serbest",
}

const MODE_BADGE_CLASS: Record<SupplyMode, string> = {
  exclusive_hq: "bg-slate-100 text-slate-700 border-slate-200",
  approved_suppliers: "bg-sky-100 text-sky-700 border-sky-200",
  free: "bg-emerald-100 text-emerald-700 border-emerald-200",
}

interface SupplyPolicyFormState {
  scope: SupplyScope
  stock_item_id: string
  category: string
  mode: SupplyMode
  approved_supplier_ids: string[]
  // Plain date input value ("YYYY-MM-DD"); converted to RFC3339 on submit.
  effective_from: string
}

const defaultForm: SupplyPolicyFormState = {
  scope: "stock_item",
  stock_item_id: "",
  category: "",
  mode: "exclusive_hq",
  approved_supplier_ids: [],
  effective_from: "",
}

function policyTarget(
  policy: SupplyPolicy,
  stockItemNames: Map<string, string>,
): string {
  if (policy.scope === "stock_item") {
    return stockItemNames.get(policy.stock_item_id ?? "") ?? policy.stock_item_id ?? "—"
  }
  if (policy.scope === "category") {
    return policy.category || "—"
  }
  return "Tüm kalemler"
}

export default function SupplyPoliciesPage() {
  const [sheetOpen, setSheetOpen] = useState(false)
  const [form, setForm] = useState<SupplyPolicyFormState>(defaultForm)

  const { data: policies, isLoading } = useSupplyPolicies()
  const { data: stockItems } = useStockItems()
  const { data: suppliers } = useParties({ type: "supplier" })
  const createPolicy = useCreateSupplyPolicy()

  const stockItemNames = new Map((stockItems ?? []).map((item) => [item.id, item.name]))
  const supplierNames = new Map((suppliers ?? []).map((party) => [party.id, party.name]))

  const handleOpen = () => {
    setForm(defaultForm)
    setSheetOpen(true)
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (form.scope === "stock_item" && !form.stock_item_id) {
      toast.error("Kapsam 'Stok Kalemi' iken bir stok kalemi seçilmelidir")
      return
    }
    if (form.scope === "category" && !form.category.trim()) {
      toast.error("Kapsam 'Kategori' iken kategori adı zorunludur")
      return
    }
    if (form.mode === "approved_suppliers" && form.approved_supplier_ids.length === 0) {
      toast.error("Mod 'Onaylı Tedarikçiler' iken en az bir tedarikçi seçilmelidir")
      return
    }
    try {
      await createPolicy.mutateAsync({
        scope: form.scope,
        stock_item_id: form.scope === "stock_item" ? form.stock_item_id : undefined,
        category: form.scope === "category" ? form.category.trim() : undefined,
        mode: form.mode,
        approved_supplier_ids:
          form.mode === "approved_suppliers" ? form.approved_supplier_ids : undefined,
        effective_from: form.effective_from
          ? new Date(form.effective_from).toISOString()
          : undefined,
      })
      toast.success("Tedarik politikası oluşturuldu")
      setSheetOpen(false)
    } catch (err) {
      const message =
        axios.isAxiosError(err) && err.response?.status === 422 && typeof err.response.data === "string"
          ? err.response.data.trim()
          : "Tedarik politikası oluşturulamadı"
      toast.error(message)
    }
  }

  const items = [...(policies ?? [])].sort((a, b) =>
    b.effective_from.localeCompare(a.effective_from),
  )

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Tedarik Politikaları</h1>
          <p className="text-muted-foreground">
            Stok kalemlerinin şubeler tarafından nasıl temin edilebileceğini tanımlayın.
          </p>
        </div>
        <Button onClick={handleOpen}>
          <Plus className="size-4" />
          Politika Ekle
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Politika Listesi</CardTitle>
          <CardDescription>
            Politikalar değiştirilemez (immutable): mevcut bir kaydı düzenleyemezsiniz. Yeni bir
            politika eklemek, aynı kapsam için önceki kaydı otomatik olarak geçersiz kılar —
            geçmiş kayıtlar tarihçe olarak saklanır, en güncel «geçerlilik başlangıcı» etkili olandır.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : items.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <ShieldCheck className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Henüz tedarik politikası tanımlanmadı</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">
                Politika tanımlanmayan kalemler varsayılan olarak «Yalnızca Merkezden» kabul edilir.
              </p>
              <Button onClick={handleOpen}>
                <Plus className="size-4" />
                İlk politikayı ekle
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Kapsam</TableHead>
                  <TableHead>Hedef</TableHead>
                  <TableHead>Mod</TableHead>
                  <TableHead>Onaylı Tedarikçiler</TableHead>
                  <TableHead>Geçerlilik Başlangıcı</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((policy) => (
                  <TableRow key={policy.id}>
                    <TableCell className="text-muted-foreground">
                      {SCOPE_LABELS[policy.scope]}
                    </TableCell>
                    <TableCell className="font-medium">
                      {policyTarget(policy, stockItemNames)}
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline" className={`gap-1 ${MODE_BADGE_CLASS[policy.mode]}`}>
                        {policy.mode === "exclusive_hq" && <Lock className="size-3" />}
                        {MODE_LABELS[policy.mode]}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {policy.mode === "approved_suppliers" && policy.approved_supplier_ids?.length
                        ? policy.approved_supplier_ids
                            .map((id) => supplierNames.get(id) ?? id)
                            .join(", ")
                        : "—"}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {new Date(policy.effective_from).toLocaleString("tr-TR")}
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
            <SheetTitle>Yeni Tedarik Politikası</SheetTitle>
            <SheetDescription>
              Politikalar immutable&apos;dır: bu form yalnızca yeni bir kayıt oluşturur, mevcut bir
              kaydı düzenlemez. Yeni kayıt, aynı kapsam için önceki politikayı geçersiz kılar.
            </SheetDescription>
          </SheetHeader>
          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="policy-scope">Kapsam</Label>
              <Select
                id="policy-scope"
                value={form.scope}
                onChange={(e) =>
                  setForm((f) => ({
                    ...f,
                    scope: e.target.value as SupplyScope,
                    stock_item_id: "",
                    category: "",
                  }))
                }
              >
                <SelectItem value="stock_item">Stok Kalemi</SelectItem>
                <SelectItem value="category">Kategori</SelectItem>
                <SelectItem value="tenant_default">Tenant Geneli (varsayılan)</SelectItem>
              </Select>
            </div>

            {form.scope === "stock_item" && (
              <div className="space-y-2">
                <Label htmlFor="policy-stock-item">Stok Kalemi</Label>
                <Select
                  id="policy-stock-item"
                  value={form.stock_item_id}
                  onChange={(e) => setForm((f) => ({ ...f, stock_item_id: e.target.value }))}
                >
                  <SelectItem value="">Stok kalemi seçin</SelectItem>
                  {(stockItems ?? []).map((item) => (
                    <SelectItem key={item.id} value={item.id}>
                      {item.name} ({item.sku})
                    </SelectItem>
                  ))}
                </Select>
              </div>
            )}

            {form.scope === "category" && (
              <div className="space-y-2">
                <Label htmlFor="policy-category">Kategori</Label>
                <Input
                  id="policy-category"
                  placeholder="örn: Kuru Gıda"
                  value={form.category}
                  onChange={(e) => setForm((f) => ({ ...f, category: e.target.value }))}
                />
              </div>
            )}

            <div className="space-y-2">
              <Label htmlFor="policy-mode">Mod</Label>
              <Select
                id="policy-mode"
                value={form.mode}
                onChange={(e) => setForm((f) => ({ ...f, mode: e.target.value as SupplyMode }))}
              >
                <SelectItem value="exclusive_hq">Yalnızca merkezden</SelectItem>
                <SelectItem value="approved_suppliers">Onaylı tedarikçiler</SelectItem>
                <SelectItem value="free">Serbest</SelectItem>
              </Select>
            </div>

            {form.mode === "approved_suppliers" && (
              <div className="space-y-2">
                <Label>Onaylı Tedarikçiler</Label>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button type="button" variant="outline" className="w-full justify-start">
                      {form.approved_supplier_ids.length > 0
                        ? `${form.approved_supplier_ids.length} tedarikçi seçildi`
                        : "Tedarikçi seçin"}
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent className="w-(--radix-dropdown-menu-trigger-width)">
                    {(suppliers ?? []).length === 0 ? (
                      <DropdownMenuLabel>Kayıtlı tedarikçi bulunamadı</DropdownMenuLabel>
                    ) : (
                      (suppliers ?? []).map((party) => (
                        <DropdownMenuCheckboxItem
                          key={party.id}
                          checked={form.approved_supplier_ids.includes(party.id)}
                          onSelect={(e) => e.preventDefault()}
                          onCheckedChange={(checked) =>
                            setForm((f) => ({
                              ...f,
                              approved_supplier_ids: checked
                                ? [...f.approved_supplier_ids, party.id]
                                : f.approved_supplier_ids.filter((id) => id !== party.id),
                            }))
                          }
                        >
                          {party.name}
                        </DropdownMenuCheckboxItem>
                      ))
                    )}
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
            )}

            <div className="space-y-2">
              <Label htmlFor="policy-effective-from">Geçerlilik Başlangıcı (opsiyonel)</Label>
              <Input
                id="policy-effective-from"
                type="date"
                value={form.effective_from}
                onChange={(e) => setForm((f) => ({ ...f, effective_from: e.target.value }))}
              />
              <p className="text-xs text-muted-foreground">
                Boş bırakılırsa politika şu andan itibaren geçerli olur.
              </p>
            </div>

            <Button type="submit" className="w-full" disabled={createPolicy.isPending}>
              {createPolicy.isPending ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </form>
        </SheetContent>
      </Sheet>
    </div>
  )
}
