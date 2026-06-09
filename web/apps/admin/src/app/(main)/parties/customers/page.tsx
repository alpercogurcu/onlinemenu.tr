"use client"

import { Plus, Users } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select } from "@/components/ui/select"
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

function partyTypeBadge(type: PartyType): string {
  switch (type) {
    case "customer":
      return "bg-blue-100 text-blue-700 border-blue-200"
    case "supplier":
      return "bg-purple-100 text-purple-700 border-purple-200"
    case "both":
      return "bg-teal-100 text-teal-700 border-teal-200"
  }
}

function partyTypeLabel(type: PartyType): string {
  const labels: Record<PartyType, string> = {
    customer: "Müşteri",
    supplier: "Tedarikçi",
    both: "İkisi de",
  }
  return labels[type]
}

interface FormState {
  name: string
  type: PartyType
  tax_number: string
}

const defaultForm: FormState = { name: "", type: "customer", tax_number: "" }

export default function CustomersPage() {
  const [sheetOpen, setSheetOpen] = useState(false)
  const [form, setForm] = useState<FormState>(defaultForm)

  const { data, isLoading } = useParties({ limit: 100 })
  const createParty = useCreateParty()

  const parties = data ?? []

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim()) {
      toast.error("Ad zorunludur")
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
      setForm(defaultForm)
    } catch {
      toast.error("Kayıt eklenemedi")
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Müşteri & Tedarikçi</h1>
          <p className="text-muted-foreground">Müşteri ve tedarikçi kayıtlarını yönetin.</p>
        </div>
        <Button onClick={() => { setForm(defaultForm); setSheetOpen(true) }}>
          <Plus className="size-4" />
          Yeni Ekle
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Kayıtlar</CardTitle>
          <CardDescription>Toplam {parties.length} kayıt.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : parties.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Users className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Kayıt bulunamadı</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">
                İlk müşteri veya tedarikçi kaydını ekleyin.
              </p>
              <Button onClick={() => { setForm(defaultForm); setSheetOpen(true) }}>
                <Plus className="size-4" />
                İlk kaydı ekle
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Ad</TableHead>
                  <TableHead>Tip</TableHead>
                  <TableHead>Vergi No</TableHead>
                  <TableHead>İletişim</TableHead>
                  <TableHead>Kayıt Tarihi</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {parties.map((party) => (
                  <TableRow key={party.id}>
                    <TableCell className="font-medium">{party.name}</TableCell>
                    <TableCell>
                      <Badge variant="outline" className={partyTypeBadge(party.type)}>
                        {partyTypeLabel(party.type)}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {party.tax_number || "—"}
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {party.contacts?.length > 0
                        ? party.contacts[0].value
                        : "—"}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {new Date(party.created_at).toLocaleDateString("tr-TR")}
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
            <SheetDescription>Müşteri veya tedarikçi ekleyin.</SheetDescription>
          </SheetHeader>
          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="party-name">Ad / Unvan</Label>
              <Input
                id="party-name"
                placeholder="Firma veya kişi adı"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="party-type">Tip</Label>
              <Select
                id="party-type"
                value={form.type}
                onChange={(e) => setForm((f) => ({ ...f, type: e.target.value as PartyType }))}
              >
                <option value="customer">Müşteri</option>
                <option value="supplier">Tedarikçi</option>
                <option value="both">İkisi de</option>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="party-tax">Vergi No</Label>
              <Input
                id="party-tax"
                placeholder="1234567890"
                value={form.tax_number}
                onChange={(e) => setForm((f) => ({ ...f, tax_number: e.target.value }))}
              />
            </div>
            <Button type="submit" className="w-full" disabled={createParty.isPending}>
              {createParty.isPending ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </form>
        </SheetContent>
      </Sheet>
    </div>
  )
}
