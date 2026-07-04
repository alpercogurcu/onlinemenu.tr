import { Package } from "lucide-react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"

export default function IntegrationsPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Entegrasyonlar</h1>
        <p className="text-muted-foreground">
          Üçüncü parti servis entegrasyonlarını yönetin.
        </p>
      </div>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {[
          { name: "Yemeksepeti", desc: "Sipariş entegrasyonu", phase: "Faz 2" },
          { name: "Trendyol Yemek", desc: "Sipariş entegrasyonu", phase: "Faz 2" },
          { name: "Getir Yemek", desc: "Sipariş entegrasyonu", phase: "Faz 2" },
          { name: "Google Business", desc: "Profil senkronizasyonu", phase: "Faz 2" },
          { name: "e-Fatura", desc: "Logo / Parasut", phase: "Faz 2" },
          { name: "ÖKC / EFT-POS", desc: "Yazar kasa entegrasyonu", phase: "Faz 2" },
        ].map((integration) => (
          <Card key={integration.name} className="opacity-60">
            <CardHeader className="pb-3">
              <div className="flex items-start justify-between">
                <div>
                  <CardTitle className="text-base">{integration.name}</CardTitle>
                  <CardDescription>{integration.desc}</CardDescription>
                </div>
                <Package className="size-5 text-muted-foreground mt-0.5" />
              </div>
            </CardHeader>
            <CardContent>
              <span className="text-xs bg-muted text-muted-foreground px-2 py-0.5 rounded-full">
                {integration.phase}&apos;de aktif
              </span>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  )
}
