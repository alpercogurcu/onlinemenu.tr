import { Warehouse } from "lucide-react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"

export default function WarehousesPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Depolar</h1>
        <p className="text-muted-foreground">Depo yönetimi Faz 2'de aktif olacak.</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Depo Yönetimi</CardTitle>
          <CardDescription>
            Birden fazla depo ve lokasyon yönetimi yakında geliyor.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex flex-col items-center justify-center py-16 text-center">
            <Warehouse className="size-12 text-muted-foreground mb-4" />
            <h3 className="text-lg font-semibold">Yakında</h3>
            <p className="text-sm text-muted-foreground mt-1 max-w-sm">
              Çok depo yönetimi, depo transfer işlemleri ve depo bazlı raporlama
              Faz 2'de kullanıma açılacak.
            </p>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
