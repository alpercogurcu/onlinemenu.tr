"use client"

import { Plus } from "lucide-react"
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
import { useCreateParty, useParties } from "@/hooks/use-parties"
import type { PartyType } from "@/types"

const partyTypeLabels: Record<PartyType, string> = {
  customer: "Müşteri",
  supplier: "Tedarikçi",
  both: "Her ikisi",
}

interface PartyFormState {
  name: string
  type: PartyType
  tax_number: string
}

const defaultForm: PartyFormState = {
  name: "",
  type: "customer",
  tax_number: "",
}

export default function PartiesPage() {
  const [sheetOpen, setSheetOpen] = useState(false)
  const [form, setForm] = useState<PartyFormState>(defaultForm)

  const { data, isLoading } = useParties()
  const createParty = useCreateParty()

  const parties = data ?? []

  const handleOpen = () => {
    setForm(defaultForm)
    setSheetOpen(true)
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim()) {
      toast.error("Ad alanı zorunludur")
      return
    }
    try {
      await createParty.mutateAsync({
        name: form.name.trim(),
        type: form.type,
        tax_number: form.tax_number.trim(),
      })
      toast.success("Kayıt eklendi")
      setSheetOpen(false)
    } catch {
      toast.error("Kayıt eklenemedi")
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Müşteriler & Tedarikçiler</h1>
          <p className="text-muted-foreground">
            İş ortaklarınızı yönetin.
          </p>
        </div>
        <Button onClick={handleOpen}>
          <Plus className="size-4" />
          Yeni Ekle
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Kayıt Listesi</CardTitle>
          <CardDescription>Tüm müşteri ve tedarikçileriniz.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-12 w-full" />
              ))}
            </div>
          ) : parties.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <p className="text-muted-foreground">Henüz kayıt eklenmedi</p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Ad</TableHead>
                  <TableHead>Tip</TableHead>
                  <TableHead>Vergi No</TableHead>
                  <TableHead className="w-[80px]">İşlemler</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {parties.map((party) => (
                  <TableRow key={party.id}>
                    <TableCell className="font-medium">{party.name}</TableCell>
                    <TableCell>
                      <Badge variant="outline">
                        {partyTypeLabels[party.type]}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {party.tax_number || "—"}
                    </TableCell>
                    <TableCell>
                      <span className="text-xs text-muted-foreground">—</span>
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
            <SheetTitle>Yeni Kayıt</SheetTitle>
            <SheetDescription>
              Yeni bir müşteri veya tedarikçi ekleyin.
            </SheetDescription>
          </SheetHeader>
          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="party-name">Ad</Label>
              <Input
                id="party-name"
                placeholder="Ad Soyad / Firma adı"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="party-type">Tip</Label>
              <Select
                id="party-type"
                value={form.type}
                onValueChange={(v) => setForm((f) => ({ ...f, type: v as PartyType }))}
              >
                <SelectItem value="customer">Müşteri</SelectItem>
                <SelectItem value="supplier">Tedarikçi</SelectItem>
                <SelectItem value="both">Her ikisi</SelectItem>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="party-tax">Vergi No</Label>
              <Input
                id="party-tax"
                placeholder="Vergi numarası"
                value={form.tax_number}
                onChange={(e) =>
                  setForm((f) => ({ ...f, tax_number: e.target.value }))
                }
              />
            </div>
            <Button
              type="submit"
              className="w-full"
              disabled={createParty.isPending}
            >
              {createParty.isPending ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </form>
        </SheetContent>
      </Sheet>
    </div>
  )
}
