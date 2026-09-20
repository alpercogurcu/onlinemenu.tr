"use client"

import { Building2, Plus } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import { FormDialog } from "@/components/layouts/form-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { branchesQueryKey, useBranches, useTenant } from "@/hooks/use-tenant"
import { useAuthStore } from "@/store/auth-store"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import api from "@/lib/api"
import {
  IDENTITY_TYPES,
  OPERATION_TYPES,
  OWNERSHIP_TYPES,
  operationLabel,
  ownershipLabel,
} from "@/lib/branch-options"
import type { Branch } from "@/types"

interface FormState {
  name: string
  operation_type: string
  ownership_type: string
  identity_type: string
}

const defaultForm: FormState = {
  name: "",
  operation_type: "restoran",
  ownership_type: "sube",
  identity_type: "kurumsal",
}

function useCreateBranch(tenantId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Partial<Branch>) =>
      api.post<Branch>(`/tenants/${tenantId}/branches/`, body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: branchesQueryKey(tenantId) })
    },
  })
}

export default function BranchesPage() {
  const [dialogOpen, setDialogOpen] = useState(false)
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
        operation_type: form.operation_type,
        ownership_type: form.ownership_type,
        identity_type: form.identity_type,
        is_active: true,
      })
      toast.success("Şube eklendi")
      setDialogOpen(false)
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
        <Button onClick={() => { setForm(defaultForm); setDialogOpen(true) }}>
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
              <Button onClick={() => { setForm(defaultForm); setDialogOpen(true) }}>
                <Plus className="size-4" />
                İlk şubeyi ekle
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Ad</TableHead>
                  <TableHead>Operasyon Tipi</TableHead>
                  <TableHead>Sahiplik</TableHead>
                  <TableHead>Durum</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {branches.map((branch) => (
                  <TableRow key={branch.id}>
                    <TableCell className="font-medium">{branch.name}</TableCell>
                    <TableCell className="text-muted-foreground">{operationLabel(branch.operation_type)}</TableCell>
                    <TableCell className="text-muted-foreground">{ownershipLabel(branch.ownership_type)}</TableCell>
                    <TableCell>
                      <Badge
                        variant="outline"
                        className={
                          branch.is_active
                            ? "bg-status-success-bg text-status-success-fg border-status-success-border"
                            : "bg-status-neutral-bg text-status-neutral-fg border-status-neutral-border"
                        }
                      >
                        {branch.is_active ? "Aktif" : "Pasif"}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <FormDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        title="Yeni Şube"
        description="İşletmenize yeni bir şube ekleyin."
        busy={createBranch.isPending}
        footer={
          <>
            <Button type="button" variant="outline" onClick={() => setDialogOpen(false)}>
              Vazgeç
            </Button>
            <Button type="submit" form="branch-form" disabled={createBranch.isPending}>
              {createBranch.isPending ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </>
        }
      >
        <form id="branch-form" onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="branch-name">Ad</Label>
            <Input
              id="branch-name"
              placeholder="örn: Merkez Şube"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            />
          </div>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="operation-type">Operasyon Tipi</Label>
              <Select
                id="operation-type"
                value={form.operation_type}
                onChange={(e) => setForm((f) => ({ ...f, operation_type: e.target.value }))}
              >
                {OPERATION_TYPES.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="ownership-type">Sahiplik</Label>
              <Select
                id="ownership-type"
                value={form.ownership_type}
                onChange={(e) => setForm((f) => ({ ...f, ownership_type: e.target.value }))}
              >
                {OWNERSHIP_TYPES.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </Select>
            </div>
          </div>
          <div className="space-y-2">
            <Label htmlFor="identity-type">Kimlik Tipi</Label>
            <Select
              id="identity-type"
              value={form.identity_type}
              onChange={(e) => setForm((f) => ({ ...f, identity_type: e.target.value }))}
            >
              {IDENTITY_TYPES.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </Select>
          </div>
        </form>
      </FormDialog>
    </div>
  )
}
