"use client"

import { Plus, UtensilsCrossed } from "lucide-react"
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
import { useModifierGroups } from "@/hooks/use-catalog"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import api from "@/lib/api"
import type { ModifierGroup } from "@/types"

interface FormState {
  name: string
  min_selections: string
  max_selections: string
  is_required: boolean
}

const defaultForm: FormState = {
  name: "",
  min_selections: "0",
  max_selections: "1",
  is_required: false,
}

function useCreateModifierGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Partial<ModifierGroup>) =>
      api.post<ModifierGroup>("/api/v1/catalog/modifier-groups", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["modifier-groups"] })
    },
  })
}

export default function ModifiersPage() {
  const [sheetOpen, setSheetOpen] = useState(false)
  const [form, setForm] = useState<FormState>(defaultForm)

  const { data, isLoading } = useModifierGroups()
  const createGroup = useCreateModifierGroup()

  const groups = data ?? []

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim()) {
      toast.error("Grup adı zorunludur")
      return
    }
    try {
      await createGroup.mutateAsync({
        name: form.name.trim(),
        min_selections: parseInt(form.min_selections) || 0,
        max_selections: parseInt(form.max_selections) || 1,
        is_required: form.is_required,
      })
      toast.success("Modifier grubu eklendi")
      setSheetOpen(false)
      setForm(defaultForm)
    } catch {
      toast.error("Grup eklenemedi")
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Modifier Grupları</h1>
          <p className="text-muted-foreground">Ürün varyasyonları ve eklentileri yönetin.</p>
        </div>
        <Button onClick={() => { setForm(defaultForm); setSheetOpen(true) }}>
          <Plus className="size-4" />
          Grup Ekle
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Modifier Grupları</CardTitle>
          <CardDescription>Boy, sos, ek malzeme gibi ürün seçenekleri.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : groups.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <UtensilsCrossed className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Henüz grup eklenmedi</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">
                Ürün seçenekleri için modifier grubu ekleyin.
              </p>
              <Button onClick={() => { setForm(defaultForm); setSheetOpen(true) }}>
                <Plus className="size-4" />
                İlk grubu ekle
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Ad</TableHead>
                  <TableHead>Min Seçim</TableHead>
                  <TableHead>Max Seçim</TableHead>
                  <TableHead>Zorunlu</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {groups.map((group) => (
                  <TableRow key={group.id}>
                    <TableCell className="font-medium">{group.name}</TableCell>
                    <TableCell>{group.min_selections}</TableCell>
                    <TableCell>{group.max_selections}</TableCell>
                    <TableCell>
                      <Badge
                        variant="outline"
                        className={
                          group.is_required
                            ? "bg-orange-100 text-orange-700 border-orange-200"
                            : "bg-gray-100 text-gray-600 border-gray-200"
                        }
                      >
                        {group.is_required ? "Zorunlu" : "Opsiyonel"}
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
            <SheetTitle>Yeni Modifier Grubu</SheetTitle>
            <SheetDescription>Ürünlere uygulanacak seçenek grubu ekleyin.</SheetDescription>
          </SheetHeader>
          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="mg-name">Ad</Label>
              <Input
                id="mg-name"
                placeholder="örn: Boy Seçimi, Sos Seçimi"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2">
                <Label htmlFor="mg-min">Min Seçim</Label>
                <Input
                  id="mg-min"
                  type="number"
                  min="0"
                  value={form.min_selections}
                  onChange={(e) => setForm((f) => ({ ...f, min_selections: e.target.value }))}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="mg-max">Max Seçim</Label>
                <Input
                  id="mg-max"
                  type="number"
                  min="1"
                  value={form.max_selections}
                  onChange={(e) => setForm((f) => ({ ...f, max_selections: e.target.value }))}
                />
              </div>
            </div>
            <div className="flex items-center gap-3">
              <Switch
                id="mg-required"
                checked={form.is_required}
                onCheckedChange={(checked) => setForm((f) => ({ ...f, is_required: checked }))}
              />
              <Label htmlFor="mg-required">Zorunlu Seçim</Label>
            </div>
            <Button type="submit" className="w-full" disabled={createGroup.isPending}>
              {createGroup.isPending ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </form>
        </SheetContent>
      </Sheet>
    </div>
  )
}
