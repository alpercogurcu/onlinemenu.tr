"use client"

import { Plus, ShoppingBag, Trash2 } from "lucide-react"
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
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  useCategories,
  useCreateProduct,
  useDeleteProduct,
  useProducts,
} from "@/hooks/use-catalog"

interface ProductFormState {
  name: string
  description: string
  base_price: string
  is_active: boolean
}

const defaultForm: ProductFormState = {
  name: "",
  description: "",
  base_price: "",
  is_active: true,
}

export default function ProductsPage() {
  const [sheetOpen, setSheetOpen] = useState(false)
  const [form, setForm] = useState<ProductFormState>(defaultForm)

  const { data, isLoading } = useProducts()
  const { data: categoriesData } = useCategories()
  const createProduct = useCreateProduct()
  const deleteProduct = useDeleteProduct()

  const categoryMap = new Map(
    (categoriesData ?? []).map((c) => [c.id, c.name]),
  )

  const handleOpen = () => {
    setForm(defaultForm)
    setSheetOpen(true)
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    const price = parseFloat(form.base_price)
    if (!form.name.trim() || isNaN(price)) {
      toast.error("Ad ve fiyat alanları zorunludur")
      return
    }
    try {
      await createProduct.mutateAsync({
        name: form.name.trim(),
        description: form.description.trim(),
        base_price: price,
        is_active: form.is_active,
      })
      toast.success("Ürün eklendi")
      setSheetOpen(false)
    } catch {
      toast.error("Ürün eklenemedi")
    }
  }

  const handleDelete = async (id: string, name: string) => {
    try {
      await deleteProduct.mutateAsync(id)
      toast.success(`"${name}" silindi`)
    } catch {
      toast.error("Ürün silinemedi")
    }
  }

  const products = data ?? []

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Ürünler</h1>
          <p className="text-muted-foreground">Menünüzdeki ürünleri yönetin.</p>
        </div>
        <Button onClick={handleOpen}>
          <Plus className="size-4" />
          Ürün Ekle
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Ürün Listesi</CardTitle>
          <CardDescription>Tüm ürünleriniz burada listelenir.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-12 w-full" />
              ))}
            </div>
          ) : products.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <ShoppingBag className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Henüz ürün eklenmedi</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">
                İlk ürününüzü ekleyerek başlayın.
              </p>
              <Button onClick={handleOpen}>
                <Plus className="size-4" />
                İlk ürünü ekle
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Ad</TableHead>
                  <TableHead>Kategori</TableHead>
                  <TableHead>Fiyat</TableHead>
                  <TableHead>Durum</TableHead>
                  <TableHead className="w-[80px]">İşlemler</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {products.map((product) => (
                  <TableRow key={product.id}>
                    <TableCell className="font-medium">{product.name}</TableCell>
                    <TableCell className="text-muted-foreground">
                      {product.category_id
                        ? (categoryMap.get(product.category_id) ?? "—")
                        : "—"}
                    </TableCell>
                    <TableCell>
                      {product.base_price.toLocaleString("tr-TR", {
                        style: "currency",
                        currency: "TRY",
                      })}
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant="outline"
                        className={
                          product.is_active
                            ? "bg-green-100 text-green-700 border-green-200"
                            : "bg-gray-100 text-gray-600 border-gray-200"
                        }
                      >
                        {product.is_active ? "Aktif" : "Pasif"}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => handleDelete(product.id, product.name)}
                        disabled={deleteProduct.isPending}
                        aria-label={`${product.name} sil`}
                      >
                        <Trash2 className="size-4 text-destructive" />
                      </Button>
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
            <SheetTitle>Yeni Ürün</SheetTitle>
            <SheetDescription>
              Menünüze yeni bir ürün ekleyin.
            </SheetDescription>
          </SheetHeader>
          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="product-name">Ad</Label>
              <Input
                id="product-name"
                placeholder="Ürün adı"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="product-desc">Açıklama</Label>
              <Input
                id="product-desc"
                placeholder="Kısa açıklama"
                value={form.description}
                onChange={(e) =>
                  setForm((f) => ({ ...f, description: e.target.value }))
                }
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="product-price">Fiyat (₺)</Label>
              <Input
                id="product-price"
                type="number"
                min="0"
                step="0.01"
                placeholder="0.00"
                value={form.base_price}
                onChange={(e) =>
                  setForm((f) => ({ ...f, base_price: e.target.value }))
                }
              />
            </div>
            <div className="flex items-center gap-3">
              <Switch
                id="product-active"
                checked={form.is_active}
                onCheckedChange={(checked) =>
                  setForm((f) => ({ ...f, is_active: checked }))
                }
              />
              <Label htmlFor="product-active">Aktif</Label>
            </div>
            <Button
              type="submit"
              className="w-full"
              disabled={createProduct.isPending}
            >
              {createProduct.isPending ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </form>
        </SheetContent>
      </Sheet>
    </div>
  )
}
