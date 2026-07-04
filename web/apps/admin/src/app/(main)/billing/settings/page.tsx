import { Settings } from "lucide-react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"

export default function BillingSettingsPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Fatura Sağlayıcı Ayarları</h1>
        <p className="text-muted-foreground">e-Fatura ve e-Arşiv entegrasyonlarını yapılandırın.</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Entegrasyon Yapılandırması</CardTitle>
          <CardDescription>
            Logo, Parasut, Mikro veya diğer e-Fatura sağlayıcısı bağlantı ayarları.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex flex-col items-center justify-center py-16 text-center">
            <Settings className="size-12 text-muted-foreground mb-4" />
            <h3 className="text-lg font-semibold">Yakında</h3>
            <p className="text-sm text-muted-foreground mt-1 max-w-sm">
              e-Fatura ve e-Arşiv sağlayıcı yapılandırması Faz 2&apos;de aktif olacak.
              Şu an mock sağlayıcı üzerinden faturalar oluşturulmaktadır.
            </p>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
