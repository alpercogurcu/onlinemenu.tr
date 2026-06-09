"use client"

import { ChefHat } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useAcceptOrder, useAdvanceOrder, useChecks, useCheckOrders } from "@/hooks/use-pos"
import type { OrderStatus } from "@/types"
import { toast } from "sonner"

function orderStatusClass(status: OrderStatus): string {
  switch (status) {
    case "pending":
      return "bg-yellow-100 text-yellow-700 border-yellow-200"
    case "accepted":
      return "bg-blue-100 text-blue-700 border-blue-200"
    case "preparing":
      return "bg-orange-100 text-orange-700 border-orange-200"
    case "ready":
      return "bg-green-100 text-green-700 border-green-200"
    case "delivered":
      return "bg-gray-100 text-gray-600 border-gray-200"
    case "rejected":
      return "bg-red-100 text-red-700 border-red-200"
    case "cancelled":
      return "bg-gray-100 text-gray-600 border-gray-200"
  }
}

function orderStatusLabel(status: OrderStatus): string {
  const labels: Record<OrderStatus, string> = {
    pending: "Bekliyor",
    accepted: "Kabul",
    preparing: "Hazırlanıyor",
    ready: "Hazır",
    delivered: "Teslim",
    rejected: "Reddedildi",
    cancelled: "İptal",
  }
  return labels[status]
}

function CheckOrdersCard({ checkId, tableLabel }: { checkId: string; tableLabel: string }) {
  const { data, isLoading } = useCheckOrders(checkId)
  const acceptOrder = useAcceptOrder()
  const advanceOrder = useAdvanceOrder()

  const activeOrders = (data ?? []).filter(
    (o) => o.status !== "delivered" && o.status !== "rejected",
  )

  if (isLoading || activeOrders.length === 0) return null

  const handleAccept = async (id: string) => {
    try {
      await acceptOrder.mutateAsync(id)
    } catch {
      toast.error("İşlem başarısız")
    }
  }

  const handleAdvance = async (id: string) => {
    try {
      await advanceOrder.mutateAsync(id)
    } catch {
      toast.error("İşlem başarısız")
    }
  }

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="text-base">{tableLabel}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {activeOrders.map((order) => (
          <div key={order.id} className="border rounded-md p-3 space-y-2">
            <div className="flex items-center justify-between">
              <Badge variant="outline" className={orderStatusClass(order.status)}>
                {orderStatusLabel(order.status)}
              </Badge>
              <span className="text-xs text-muted-foreground">
                {new Date(order.created_at).toLocaleTimeString("tr-TR")}
              </span>
            </div>
            <ul className="space-y-1">
              {order.items.map((item) => (
                <li key={item.id} className="text-sm flex justify-between">
                  <span>{item.product_name}</span>
                  <span className="text-muted-foreground">×{item.quantity}</span>
                </li>
              ))}
            </ul>
            <div className="flex gap-2 pt-1">
              {order.status === "pending" && (
                <Button
                  size="sm"
                  className="h-7 text-xs"
                  onClick={() => handleAccept(order.id)}
                  disabled={acceptOrder.isPending}
                >
                  Kabul Et
                </Button>
              )}
              {(order.status === "accepted" || order.status === "preparing") && (
                <Button
                  size="sm"
                  variant="outline"
                  className="h-7 text-xs"
                  onClick={() => handleAdvance(order.id)}
                  disabled={advanceOrder.isPending}
                >
                  {order.status === "accepted" ? "Hazırlamaya Başla" : "Hazır"}
                </Button>
              )}
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

export default function KitchenPage() {
  const { data, isLoading } = useChecks(
    { status: "open", refetchInterval: 15_000 } as Parameters<typeof useChecks>[0],
  )

  const openChecks = data ?? []

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Mutfak Ekranı</h1>
          <p className="text-muted-foreground">Aktif siparişleri takip edin.</p>
        </div>
        <Badge variant="outline" className="bg-amber-100 text-amber-700 border-amber-200">
          {openChecks.length} açık masa
        </Badge>
      </div>

      {isLoading ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-48 rounded-lg" />
          ))}
        </div>
      ) : openChecks.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-16 text-center">
          <ChefHat className="size-12 text-muted-foreground mb-4" />
          <h3 className="text-lg font-semibold">Aktif sipariş yok</h3>
          <p className="text-sm text-muted-foreground mt-1">
            Yeni sipariş geldiğinde burada görünecek.
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {openChecks.map((check) => (
            <CheckOrdersCard
              key={check.id}
              checkId={check.id}
              tableLabel={check.table_label}
            />
          ))}
        </div>
      )}
    </div>
  )
}
