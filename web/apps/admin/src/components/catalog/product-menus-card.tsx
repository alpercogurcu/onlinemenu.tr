"use client"

import { useTranslations } from "next-intl"
import { toast } from "sonner"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Switch } from "@/components/ui/switch"
import { useAddMenuItem, useRemoveMenuItem } from "@/hooks/use-catalog"
import type { Menu } from "@/types"

interface ProductMenusCardProps {
  productId: string
  menus: Menu[]
  // Menu ids that already carry this product — computed once in
  // product-editor.tsx from a batched useQueries over every menu's items
  // (see there), so this card does not re-fetch per row.
  menuIdsWithProduct: Set<string>
}

export function ProductMenusCard({ productId, menus, menuIdsWithProduct }: ProductMenusCardProps) {
  const t = useTranslations("catalog.product.menus")
  const tItems = useTranslations("catalogMenus.items")

  const addItem = useAddMenuItem()
  const removeItem = useRemoveMenuItem()

  async function handleToggle(menuId: string, nextOn: boolean) {
    try {
      if (nextOn) {
        await addItem.mutateAsync({ menuId, product_id: productId, price_override: null, is_active: true })
        toast.success(tItems("added"))
      } else {
        await removeItem.mutateAsync({ menuId, productId })
        toast.success(tItems("removed"))
      }
    } catch {
      toast.error(nextOn ? tItems("addFailed") : tItems("removeFailed"))
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("title")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {menus.map((menu) => {
          const isOn = menuIdsWithProduct.has(menu.id)
          return (
            <div key={menu.id} className="flex items-start justify-between gap-3 rounded-md border p-3">
              <div className="min-w-0 space-y-1">
                <p className="font-medium">{menu.name}</p>
                {menu.description ? <p className="text-xs text-muted-foreground">{menu.description}</p> : null}
                {isOn ? <p className="text-xs text-muted-foreground">{t("priceDefault")}</p> : null}
              </div>
              <Switch
                checked={isOn}
                aria-label={menu.name}
                disabled={addItem.isPending || removeItem.isPending}
                onCheckedChange={(checked) => void handleToggle(menu.id, checked)}
              />
            </div>
          )
        })}
        <p className="text-xs text-muted-foreground">{t("hint")}</p>
      </CardContent>
    </Card>
  )
}
