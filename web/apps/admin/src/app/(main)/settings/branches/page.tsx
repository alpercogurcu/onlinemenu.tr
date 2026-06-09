"use client"

import { Building2, Plus } from "lucide-react"
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
import { useBranches, useTenant } from "@/hooks/use-tenant"
import { useAuthStore } from "@/store/auth-store"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import api from "@/lib/api"
import type { Branch } from "@/types"

interface FormState {
  name: string
  branch_type: string
  ownership_type: string
}

const defaultForm: FormState = {
  name: "",
  branch_type: "dine_in",
  ownership_type: "owned",
}

function useCreateBranch(tenantId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Partial<Branch>) =>
      api.post<Branch>(`/tenants/${tenantId}/branches/`, body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["branches", tenantId] })
    },
  })
}

export default function BranchesPage() {
  const [sheetOpen, setSheetOpen] = useState(false)
  const [form, setForm] = useState<FormState>(defaultForm)
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""

  const { data: branches = [], isLoading } = useBranches(tenantId)
  const createBranch = useCreateBranch(tenantId)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim()) {
      toast.error("Şube adı zorunludur")
      return
    }
    try {
      await createBranch.mutateAsync({
        name: form.name.trim(),
        branch_type: form.branch_type,
        ownership_type: form.ownership_type,
        is_active: true,
      })
      toast.success("Şube eklendi")
      setSheetOpen(false)
      setForm(defaultForm)
    } catch {
      toast.error("Şube eklenemedi")
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Şubeler</h1>
          <p className="text-muted-foreground">İşletme şubelerini yönetin.</p>
        </div>
        <Button onClick={() => { setForm(defaultForm); setSheetOpen(true) }}>
          <Plus className="size-4" />
          Şube Ekle
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Şube Listesi</CardTitle>
          <CardDescription>Toplam {branches.length} şube.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : branches.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Building2 className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Şube bulunamadı</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">
                İşletmenizin şubelerini ekleyin.
              </p>
              <Button onClick={() => { setForm(defaultForm); setSheetOpen(true) }}>
                <Plus className="size-4" />
                İlk şubeyi ekle
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Ad</TableHead>
                  <TableHead>Tip</TableHead>
                  <TableHead>Sahiplik</TableHead>
                  <TableHead>Durum</TableHead>
                  <TableHead>Oluşturulma</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {branches.map((branch) => (
                  <TableRow key={branch.id}>
                    <TableCell className="font-medium">{branch.name}</TableCell>
                    <TableCell className="text-muted-foreground">{branch.branch_type}</TableCell>
                    <TableCell className="text-muted-foreground">{branch.ownership_type}</TableCell>
                    <TableCell>
                      <Badge
                        variant="outline"
                        className={
                          branch.is_active
                            ? "bg-green-100 text-green-700 border-green-200"
                            : "bg-gray-100 text-gray-600 border-gray-200"
                        }
                      >
                        {branch.is_active ? "Aktif" : "Pasif"}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {new Date(branch.created_at).toLocaleDateString("tr-TR")}
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
            <SheetTitle>Yeni Şube</SheetTitle>
            <SheetDescription>İşletmenize yeni bir şube ekleyin.</SheetDescription>
          </SheetHeader>
          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="branch-name">Ad</Label>
              <Input
                id="branch-name"
                placeholder="örn: Merkez Şube"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="branch-type">Şube Tipi</Label>
              <Select
                id="branch-type"
                value={form.branch_type}
                onChange={(e) => setForm((f) => ({ ...f, branch_type: e.target.value }))}
              >
                <option value="dine_in">Restoran (İç Mekan)</option>
                <option value="takeaway">Paket Servis</option>
                <option value="delivery">Teslimat</option>
                <option value="cloud_kitchen">Bulut Mutfak</option>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="ownership-type">Sahiplik</Label>
              <Select
                id="ownership-type"
                value={form.ownership_type}
                onChange={(e) => setForm((f) => ({ ...f, ownership_type: e.target.value }))}
              >
                <option value="owned">Mülk Sahibi</option>
                <option value="franchised">Franchise</option>
                <option value="licensed">Lisanslı</option>
              </Select>
            </div>
            <Button type="submit" className="w-full" disabled={createBranch.isPending}>
              {createBranch.isPending ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </form>
        </SheetContent>
      </Sheet>
    </div>
  )
}
