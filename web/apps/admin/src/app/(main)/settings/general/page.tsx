"use client"

import { Settings } from "lucide-react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { useTenant } from "@/hooks/use-tenant"
import { useAuthStore } from "@/store/auth-store"

export default function GeneralSettingsPage() {
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""

  const { data: tenant, isLoading } = useTenant(tenantId)

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Genel Ayarlar</h1>
        <p className="text-muted-foreground">İşletme bilgilerini görüntüleyin ve yönetin.</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>İşletme Bilgileri</CardTitle>
          <CardDescription>Temel işletme bilgileri.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => <Skeleton key={i} className="h-10 w-full" />)}
            </div>
          ) : !tenant ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Settings className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">İşletme bilgisi yüklenemedi</h3>
            </div>
          ) : (
            <>
              <div className="space-y-2">
                <Label>İşletme Adı</Label>
                <Input value={tenant.name} readOnly className="bg-muted" />
              </div>
              <div className="space-y-2">
                <Label>Slug</Label>
                <Input value={tenant.slug} readOnly className="bg-muted" />
              </div>
              <div className="space-y-2">
                <Label>Plan</Label>
                <Input value={tenant.plan} readOnly className="bg-muted" />
              </div>
              <div className="space-y-2">
                <Label>Oluşturulma Tarihi</Label>
                <Input
                  value={new Date(tenant.created_at).toLocaleDateString("tr-TR")}
                  readOnly
                  className="bg-muted"
                />
              </div>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
