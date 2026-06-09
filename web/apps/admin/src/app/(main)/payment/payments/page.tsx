"use client"

import { CreditCard } from "lucide-react"

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
import type { Payment, PaymentMethod } from "@/types"

function usePayments(params?: { limit?: number; offset?: number }) {
  return useQuery({
    queryKey: ["payments", params],
    queryFn: async () => {
      const { data } = await api.get<Payment[]>("/api/v1/payments/", { params })
      return data ?? []
    },
  })
}

function methodBadgeClass(method: PaymentMethod): string {
  switch (method) {
    case "cash":
      return "bg-green-100 text-green-700 border-green-200"
    case "card":
      return "bg-blue-100 text-blue-700 border-blue-200"
    case "online":
      return "bg-purple-100 text-purple-700 border-purple-200"
  }
}

function methodLabel(method: PaymentMethod): string {
  const labels: Record<PaymentMethod, string> = {
    cash: "Nakit",
    card: "Kart",
    online: "Online",
  }
  return labels[method]
}

export default function PaymentsPage() {
  const { data, isLoading } = usePayments({ limit: 50 })

  const payments = data ?? []
  const totalAmount = payments.reduce((sum, p) => sum + p.amount, 0)

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Ödemeler</h1>
          <p className="text-muted-foreground">Tüm ödeme işlemlerini görüntüleyin.</p>
        </div>
        {payments.length > 0 && (
          <div className="text-right">
            <p className="text-sm text-muted-foreground">Toplam</p>
            <p className="text-lg font-bold">
              {totalAmount.toLocaleString("tr-TR", { style: "currency", currency: "TRY" })}
            </p>
          </div>
        )}
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Ödeme Listesi</CardTitle>
          <CardDescription>Son 50 ödeme işlemi.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : payments.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <CreditCard className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Ödeme bulunamadı</h3>
              <p className="text-sm text-muted-foreground mt-1">
                Ödeme işlemleri gerçekleştikçe burada görünür.
              </p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Adisyon</TableHead>
                  <TableHead>Tutar</TableHead>
                  <TableHead>Yöntem</TableHead>
                  <TableHead>Durum</TableHead>
                  <TableHead>Tarih</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {payments.map((payment) => (
                  <TableRow key={payment.id}>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {payment.check_id.slice(0, 8)}…
                    </TableCell>
                    <TableCell className="font-semibold">
                      {payment.amount.toLocaleString("tr-TR", {
                        style: "currency",
                        currency: "TRY",
                      })}
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline" className={methodBadgeClass(payment.method)}>
                        {methodLabel(payment.method)}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant="outline"
                        className={
                          payment.status === "completed"
                            ? "bg-green-100 text-green-700 border-green-200"
                            : payment.status === "failed"
                              ? "bg-red-100 text-red-700 border-red-200"
                              : "bg-gray-100 text-gray-600 border-gray-200"
                        }
                      >
                        {payment.status}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {new Date(payment.created_at).toLocaleString("tr-TR")}
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
