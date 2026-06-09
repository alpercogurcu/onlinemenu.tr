"use client"

import { ClipboardList } from "lucide-react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useQuery } from "@tanstack/react-query"
import api from "@/lib/api"
import { useAuthStore } from "@/store/auth-store"

interface Role {
  id: string
  name: string
  scope: string
  system_key: string
  is_system: boolean
  branch_id?: string
}

interface RoleListResponse {
  roles: Role[]
}

function useRoles(tenantId: string) {
  return useQuery({
    queryKey: ["roles", tenantId],
    queryFn: async () => {
      const { data } = await api.get<RoleListResponse>(`/v1/identity/${tenantId}/roles`)
      return data
    },
    enabled: Boolean(tenantId),
  })
}

export default function RolesPage() {
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""

  const { data, isLoading } = useRoles(tenantId)
  const roles = data?.roles ?? []

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Roller</h1>
        <p className="text-muted-foreground">Kullanıcı rol ve izinlerini görüntüleyin.</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Rol Listesi</CardTitle>
          <CardDescription>Sistemde tanımlı roller.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : roles.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <ClipboardList className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Rol bulunamadı</h3>
              <p className="text-sm text-muted-foreground mt-1">
                Rol tanımları kimlik servisinden yüklenecek.
              </p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Ad</TableHead>
                  <TableHead>Kapsam</TableHead>
                  <TableHead>Tip</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {roles.map((role) => (
                  <TableRow key={role.id}>
                    <TableCell className="font-medium">{role.name}</TableCell>
                    <TableCell className="text-muted-foreground text-xs font-mono">{role.scope}</TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {role.is_system ? "Sistem" : "Özel"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
