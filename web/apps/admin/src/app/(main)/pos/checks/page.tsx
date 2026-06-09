"use client"

import { ClipboardList } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useCancelCheck, useChecks, useCloseCheck } from "@/hooks/use-pos"
import type { CheckStatus } from "@/types"
import { toast } from "sonner"

function statusBadgeClass(status: CheckStatus): string {
  switch (status) {
    case "open":
      return "bg-amber-100 text-amber-700 border-amber-200"
    case "closed":
      return "bg-green-100 text-green-700 border-green-200"
    case "cancelled":
      return "bg-gray-100 text-gray-600 border-gray-200"
  }
}

function statusLabel(status: CheckStatus): string {
  switch (status) {
    case "open":
      return "Açık"
    case "closed":
      return "Kapalı"
    case "cancelled":
      return "İptal"
  }
}

export default function ChecksPage() {
  const { data, isLoading } = useChecks({ refetchInterval: 30_000 } as Parameters<typeof useChecks>[0])
  const closeCheck = useCloseCheck()
  const cancelCheck = useCancelCheck()

  const checks = data ?? []

  const handleClose = async (id: string, label: string) => {
    try {
      await closeCheck.mutateAsync(id)
      toast.success(`"${label}" adisyonu kapatıldı`)
    } catch {
      toast.error("Adisyon kapatılamadı")
    }
  }

  const handleCancel = async (id: string, label: string) => {
    try {
      await cancelCheck.mutateAsync(id)
      toast.success(`"${label}" adisyonu iptal edildi`)
    } catch {
      toast.error("Adisyon iptal edilemedi")
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Adisyonlar</h1>
        <p className="text-muted-foreground">Tüm adisyonları görüntüleyin ve yönetin.</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Adisyon Listesi</CardTitle>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-12 w-full" />
              ))}
            </div>
          ) : checks.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <ClipboardList className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Adisyon bulunamadı</h3>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Masa</TableHead>
                  <TableHead>Not</TableHead>
                  <TableHead>Durum</TableHead>
                  <TableHead>Açılış</TableHead>
                  <TableHead>Kapanış</TableHead>
                  <TableHead className="w-[160px]">İşlemler</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {checks.map((check) => (
                  <TableRow key={check.id}>
                    <TableCell className="font-medium">{check.table_label}</TableCell>
                    <TableCell className="text-muted-foreground text-xs">{check.note || "—"}</TableCell>
                    <TableCell>
                      <Badge variant="outline" className={statusBadgeClass(check.status)}>
                        {statusLabel(check.status)}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {new Date(check.opened_at).toLocaleString("tr-TR")}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {check.closed_at
                        ? new Date(check.closed_at).toLocaleString("tr-TR")
                        : "—"}
                    </TableCell>
                    <TableCell>
                      {check.status === "open" && (
                        <div className="flex gap-2">
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => handleClose(check.id, check.table_label)}
                            disabled={closeCheck.isPending}
                          >
                            Kapat
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => handleCancel(check.id, check.table_label)}
                            disabled={cancelCheck.isPending}
                            className="text-destructive hover:text-destructive"
                          >
                            İptal
                          </Button>
                        </div>
                      )}
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
