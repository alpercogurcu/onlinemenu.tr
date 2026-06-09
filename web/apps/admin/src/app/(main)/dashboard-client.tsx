"use client"

import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useProducts } from "@/hooks/use-catalog"
import { useChecks } from "@/hooks/use-pos"

const mockSalesData = [
  { day: "Pzt", satis: 4200 },
  { day: "Sal", satis: 3800 },
  { day: "Çar", satis: 5100 },
  { day: "Per", satis: 4700 },
  { day: "Cum", satis: 6300 },
  { day: "Cmt", satis: 8200 },
  { day: "Paz", satis: 7100 },
]

export default function DashboardClient() {
  const openChecks = useChecks({ status: "open", limit: 100 })
  const allProducts = useProducts({ limit: 5 })

  const openCheckCount = openChecks.data?.length ?? 0
  const productCount = allProducts.data?.length ?? 0

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Genel Bakış</h1>
        <p className="text-muted-foreground">
          İşletmenizin günlük performansını takip edin.
        </p>
      </div>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader>
            <CardDescription>Açık Adisyon</CardDescription>
            {openChecks.isLoading ? (
              <Skeleton className="h-9 w-16" />
            ) : (
              <CardTitle className="text-3xl">{openCheckCount}</CardTitle>
            )}
          </CardHeader>
          <CardContent>
            <p className="text-xs text-muted-foreground">Şu anda açık</p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardDescription>Toplam Ürün</CardDescription>
            {allProducts.isLoading ? (
              <Skeleton className="h-9 w-16" />
            ) : (
              <CardTitle className="text-3xl">{productCount}</CardTitle>
            )}
          </CardHeader>
          <CardContent>
            <p className="text-xs text-muted-foreground">Katalogdaki ürünler</p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardDescription>Günlük Sipariş</CardDescription>
            <CardTitle className="text-3xl text-muted-foreground">—</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-xs text-muted-foreground">Yakında</p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardDescription>Stok Uyarısı</CardDescription>
            <CardTitle className="text-3xl text-muted-foreground">—</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-xs text-muted-foreground">Yakında</p>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Haftalık Satış</CardTitle>
          <CardDescription>Son 7 günün satış grafiği</CardDescription>
        </CardHeader>
        <CardContent>
          <ResponsiveContainer width="100%" height={300}>
            <AreaChart data={mockSalesData}>
              <defs>
                <linearGradient id="colorSatis" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--color-primary)" stopOpacity={0.3} />
                  <stop offset="95%" stopColor="var(--color-primary)" stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid strokeDasharray="3 3" className="stroke-border" />
              <XAxis dataKey="day" className="text-xs" />
              <YAxis className="text-xs" />
              <Tooltip
                formatter={(value: number) => [`₺${value.toLocaleString("tr-TR")}`, "Satış"]}
              />
              <Area
                type="monotone"
                dataKey="satis"
                stroke="var(--color-primary)"
                strokeWidth={2}
                fill="url(#colorSatis)"
              />
            </AreaChart>
          </ResponsiveContainer>
        </CardContent>
      </Card>
    </div>
  )
}
