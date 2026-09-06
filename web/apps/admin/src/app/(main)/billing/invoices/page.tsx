"use client"

import { FileText, RotateCcw } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
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
import { useInvoices, useRetryInvoice } from "@/hooks/use-billing"
import type { InvoiceStatus } from "@/types"

function statusBadgeClass(status: InvoiceStatus): string {
  switch (status) {
    case "sent":
      return "bg-status-success-bg text-status-success-fg border-status-success-border"
    case "pending":
      return "bg-status-warning-bg text-status-warning-fg border-status-warning-border"
    case "failed":
      return "bg-status-danger-bg text-status-danger-fg border-status-danger-border"
    case "cancelled":
      return "bg-status-neutral-bg text-status-neutral-fg border-status-neutral-border"
  }
}

function statusLabel(status: InvoiceStatus): string {
  const labels: Record<InvoiceStatus, string> = {
    pending: "Bekliyor",
    sent: "Gönderildi",
    failed: "Başarısız",
    cancelled: "İptal",
  }
  return labels[status]
}

export default function InvoicesPage() {
  const { data, isLoading } = useInvoices({ limit: 50 })
  const retryInvoice = useRetryInvoice()

  const invoices = data ?? []
  const failedCount = invoices.filter((i) => i.status === "failed").length

  const handleRetry = async (id: string) => {
    try {
      await retryInvoice.mutateAsync(id)
      toast.success("Fatura yeniden gönderildi")
    } catch {
      toast.error("Yeniden gönderim başarısız")
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Faturalar</h1>
          <p className="text-muted-foreground">e-Fatura ve e-Arşiv işlemlerini takip edin.</p>
        </div>
        {failedCount > 0 && (
          <Badge variant="outline" className="bg-status-danger-bg text-status-danger-fg border-status-danger-border">
            {failedCount} başarısız
          </Badge>
        )}
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Fatura Listesi</CardTitle>
          <CardDescription>Son 50 fatura işlemi.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : invoices.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <FileText className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Fatura bulunamadı</h3>
              <p className="text-sm text-muted-foreground mt-1">
                Ödeme yapıldığında faturalar otomatik oluşturulur.
              </p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Ödeme ID</TableHead>
                  <TableHead>Tutar</TableHead>
                  <TableHead>Sağlayıcı</TableHead>
                  <TableHead>Durum</TableHead>
                  <TableHead>Tarih</TableHead>
                  <TableHead className="w-[80px]">İşlem</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {invoices.map((invoice) => (
                  <TableRow key={invoice.id}>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {invoice.payment_id.slice(0, 8)}…
                    </TableCell>
                    <TableCell className="font-semibold">
                      {invoice.amount.toLocaleString("tr-TR", {
                        style: "currency",
                        currency: "TRY",
                      })}
                    </TableCell>
                    <TableCell className="text-muted-foreground">{invoice.provider}</TableCell>
                    <TableCell>
                      <Badge variant="outline" className={statusBadgeClass(invoice.status)}>
                        {statusLabel(invoice.status)}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {new Date(invoice.created_at).toLocaleString("tr-TR")}
                    </TableCell>
                    <TableCell>
                      {invoice.status === "failed" && (
                        <Button
                          variant="ghost"
                          size="icon"
                          onClick={() => handleRetry(invoice.id)}
                          disabled={retryInvoice.isPending}
                          aria-label="Yeniden gönder"
                        >
                          <RotateCcw className="size-4" />
                        </Button>
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
