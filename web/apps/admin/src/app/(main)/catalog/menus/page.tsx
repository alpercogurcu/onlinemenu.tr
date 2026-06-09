"use client"

import { FileText, Plus } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
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
import { useMenus } from "@/hooks/use-catalog"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import api from "@/lib/api"
import type { Menu } from "@/types"

interface FormState {
  name: string
  description: string
  is_active: boolean
}

const defaultForm: FormState = { name: "", description: "", is_active: true }

function useCreateMenu() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Partial<Menu>) => api.post<Menu>("/api/v1/catalog/menus", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["menus"] })
    },
  })
}

export default function MenusPage() {
  const [sheetOpen, setSheetOpen] = useState(false)
  const [form, setForm] = useState<FormState>(defaultForm)

  const { data, isLoading } = useMenus()
  const createMenu = useCreateMenu()

  const menus = data ?? []

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim()) {
      toast.error("Menü adı zorunludur")
      return
    }
    try {
      await createMenu.mutateAsync({
        name: form.name.trim(),
        description: form.description.trim(),
        is_active: form.is_active,
      })
      toast.success("Menü eklendi")
      setSheetOpen(false)
      setForm(defaultForm)
    } catch {
      toast.error("Menü eklenemedi")
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Menüler</h1>
          <p className="text-muted-foreground">Dijital menü yapılandırmalarını yönetin.</p>
        </div>
        <Button onClick={() => { setForm(defaultForm); setSheetOpen(true) }}>
          <Plus className="size-4" />
          Menü Ekle
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Menü Listesi</CardTitle>
          <CardDescription>Kahvaltı, öğle, akşam gibi farklı menü tipleri tanımlayın.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : menus.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <FileText className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Henüz menü eklenmedi</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">
                İşletmenizin menülerini oluşturun.
              </p>
              <Button onClick={() => { setForm(defaultForm); setSheetOpen(true) }}>
                <Plus className="size-4" />
                İlk menüyü ekle
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Ad</TableHead>
                  <TableHead>Açıklama</TableHead>
                  <TableHead>Durum</TableHead>
                  <TableHead>Oluşturulma</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {menus.map((menu) => (
                  <TableRow key={menu.id}>
                    <TableCell className="font-medium">{menu.name}</TableCell>
                    <TableCell className="text-muted-foreground">{menu.description || "—"}</TableCell>
                    <TableCell>
                      <Badge
                        variant="outline"
                        className={
                          menu.is_active
                            ? "bg-green-100 text-green-700 border-green-200"
                            : "bg-gray-100 text-gray-600 border-gray-200"
                        }
                      >
                        {menu.is_active ? "Aktif" : "Pasif"}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {new Date(menu.created_at).toLocaleDateString("tr-TR")}
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
            <SheetTitle>Yeni Menü</SheetTitle>
            <SheetDescription>İşletmenize yeni bir menü ekleyin.</SheetDescription>
          </SheetHeader>
          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="menu-name">Ad</Label>
              <Input
                id="menu-name"
                placeholder="örn: Kahvaltı Menüsü"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="menu-desc">Açıklama</Label>
              <Input
                id="menu-desc"
                placeholder="Kısa açıklama"
                value={form.description}
                onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
              />
            </div>
            <div className="flex items-center gap-3">
              <Switch
                id="menu-active"
                checked={form.is_active}
                onCheckedChange={(checked) => setForm((f) => ({ ...f, is_active: checked }))}
              />
              <Label htmlFor="menu-active">Aktif</Label>
            </div>
            <Button type="submit" className="w-full" disabled={createMenu.isPending}>
              {createMenu.isPending ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </form>
        </SheetContent>
      </Sheet>
    </div>
  )
}
