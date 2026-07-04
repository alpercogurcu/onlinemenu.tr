"use client"

import { Users } from "lucide-react"

import { Badge } from "@/components/ui/badge"
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

interface Membership {
  id: string
  person_id: string
  role_id: string
  tenant_id: string
  branch_id?: string
  status: string
}

interface MembershipListResponse {
  memberships: Membership[]
}

function useMembers(tenantId: string) {
  return useQuery({
    queryKey: ["memberships", tenantId],
    queryFn: async () => {
      const { data } = await api.get<MembershipListResponse>(`/v1/identity/${tenantId}/memberships`)
      return data
    },
    enabled: Boolean(tenantId),
  })
}

export default function UsersPage() {
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""

  const { data, isLoading } = useMembers(tenantId)
  const members = data?.memberships ?? []

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Kullanıcılar</h1>
        <p className="text-muted-foreground">İşletme üyelerini ve erişim yetkilerini yönetin.</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Üyeler</CardTitle>
          <CardDescription>Toplam {members.length} üye.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : members.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Users className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Üye bulunamadı</h3>
              <p className="text-sm text-muted-foreground mt-1">
                Kullanıcı davetleri Faz 2&apos;de aktif olacak.
              </p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Kişi ID</TableHead>
                  <TableHead>Rol</TableHead>
                  <TableHead>Durum</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {members.map((member) => (
                  <TableRow key={member.id}>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {member.person_id.slice(0, 8)}…
                    </TableCell>
                    <TableCell className="text-muted-foreground text-xs font-mono">
                      {member.role_id.slice(0, 8)}…
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant="outline"
                        className={
                          member.status === "active"
                            ? "bg-green-100 text-green-700 border-green-200"
                            : "bg-gray-100 text-gray-600 border-gray-200"
                        }
                      >
                        {member.status === "active" ? "Aktif" : member.status}
                      </Badge>
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
