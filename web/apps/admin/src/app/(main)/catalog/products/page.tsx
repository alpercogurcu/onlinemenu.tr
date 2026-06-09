import type { Metadata } from "next"
import { Plus, ShoppingBag } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"

export const metadata: Metadata = {
  title: "Ürünler",
}

export default function ProductsPage() {
  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Ürünler</h1>
          <p className="text-muted-foreground">
            Menünüzdeki ürünleri yönetin.
          </p>
        </div>
        <Button>
          <Plus className="size-4" />
          Ürün Ekle
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Ürün Listesi</CardTitle>
          <CardDescription>Tüm ürünleriniz burada listelenir.</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex flex-col items-center justify-center py-16 text-center">
            <ShoppingBag className="size-12 text-muted-foreground mb-4" />
            <h3 className="text-lg font-semibold">Henüz ürün eklenmedi</h3>
            <p className="text-sm text-muted-foreground mt-1 mb-4">
              İlk ürününüzü ekleyerek başlayın.
            </p>
            <Button>
              <Plus className="size-4" />
              Ürün Ekle
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
