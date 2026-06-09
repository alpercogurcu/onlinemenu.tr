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

const mockSalesData = [
  { day: "Pzt", satis: 4200 },
  { day: "Sal", satis: 3800 },
  { day: "Çar", satis: 5100 },
  { day: "Per", satis: 4700 },
  { day: "Cum", satis: 6300 },
  { day: "Cmt", satis: 8200 },
  { day: "Paz", satis: 7100 },
]

const statsCards = [
  {
    title: "Bugünkü Satış",
    value: "₺12.450",
    description: "Son 24 saat",
  },
  {
    title: "Aktif Adisyon",
    value: "8",
    description: "Şu anda açık",
  },
  {
    title: "Bekleyen Sipariş",
    value: "23",
    description: "Mutfakta hazırlanıyor",
  },
  {
    title: "Toplam Müşteri",
    value: "1.284",
    description: "Bu ay",
  },
]

export default function DashboardClient() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Genel Bakış</h1>
        <p className="text-muted-foreground">
          İşletmenizin günlük performansını takip edin.
        </p>
      </div>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        {statsCards.map((card) => (
          <Card key={card.title}>
            <CardHeader>
              <CardDescription>{card.title}</CardDescription>
              <CardTitle className="text-3xl">{card.value}</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-xs text-muted-foreground">{card.description}</p>
            </CardContent>
          </Card>
        ))}
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
