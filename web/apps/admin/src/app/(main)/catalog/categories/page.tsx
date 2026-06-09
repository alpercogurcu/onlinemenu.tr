"use client"

import { Plus, Tag, Trash2 } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card"
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useCategories, useCreateCategory } from "@/hooks/use-catalog"

interface FormState {
  name: string
  description: string
  sort_order: string
}

const defaultForm: FormState = { name: "", description: "", sort_order: "0" }

export default function CategoriesPage() {
  const [sheetOpen, setSheetOpen] = useState(false)
  const [form, setForm] = useState<FormState>(defaultForm)

  const { data, isLoading } = useCategories()
  const createCategory = useCreateCategory()

  const categories = data ?? []

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim()) {
      toast.error("Kategori adı zorunludur")
      return
    }
    try {
      await createCategory.mutateAsync({
        name: form.name.trim(),
        description: form.description.trim(),
        sort_order: parseInt(form.sort_order) || 0,
        is_active: true,
      })
      toast.success("Kategori eklendi")
      setSheetOpen(false)
      setForm(defaultForm)
    } catch {
      toast.error("Kategori eklenemedi")
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Kategoriler</h1>
          <p className="text-muted-foreground">Menü kategorilerini yönetin.</p>
        </div>
        <Button onClick={() => { setForm(defaultForm); setSheetOpen(true) }}>
          <Plus className="size-4" />
          Kategori Ekle
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Kategori Listesi</CardTitle>
          <CardDescription>Ürünlerinizi gruplandıran kategoriler.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : categories.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Tag className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Henüz kategori eklenmedi</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">
                Ürünlerinizi gruplandırmak için kategori ekleyin.
              </p>
              <Button onClick={() => { setForm(defaultForm); setSheetOpen(true) }}>
                <Plus className="size-4" />
                İlk kategoriyi ekle
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Ad</TableHead>
                  <TableHead>Açıklama</TableHead>
                  <TableHead>Sıra</TableHead>
                  <TableHead>Durum</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {categories.map((cat) => (
                  <TableRow key={cat.id}>
                    <TableCell className="font-medium">{cat.name}</TableCell>
                    <TableCell className="text-muted-foreground">{cat.description || "—"}</TableCell>
                    <TableCell>{cat.sort_order}</TableCell>
                    <TableCell>
                      <Badge
                        variant="outline"
                        className={
                          cat.is_active
                            ? "bg-green-100 text-green-700 border-green-200"
                            : "bg-gray-100 text-gray-600 border-gray-200"
                        }
                      >
                        {cat.is_active ? "Aktif" : "Pasif"}
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
            <SheetTitle>Yeni Kategori</SheetTitle>
            <SheetDescription>Menünüze yeni bir kategori ekleyin.</SheetDescription>
          </SheetHeader>
          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="cat-name">Ad</Label>
              <Input
                id="cat-name"
                placeholder="Kategori adı"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="cat-desc">Açıklama</Label>
              <Input
                id="cat-desc"
                placeholder="Kısa açıklama"
                value={form.description}
                onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="cat-order">Sıralama</Label>
              <Input
                id="cat-order"
                type="number"
                min="0"
                placeholder="0"
                value={form.sort_order}
                onChange={(e) => setForm((f) => ({ ...f, sort_order: e.target.value }))}
              />
            </div>
            <Button type="submit" className="w-full" disabled={createCategory.isPending}>
              {createCategory.isPending ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </form>
        </SheetContent>
      </Sheet>
    </div>
  )
}
