"use client"

import { Loader2, Plus, Trash2 } from "lucide-react"
import { useTranslations } from "next-intl"
import { useState } from "react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectItem } from "@/components/ui/select"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useAddMenuItem, useMenuItems, useProducts, useRemoveMenuItem } from "@/hooks/use-catalog"
import { formatKurus, formatKurusForInput, parseLiraToKurus } from "@/lib/money"
import type { Menu, Product } from "@/types"

interface MenuItemsSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  menu: Menu
}

export function MenuItemsSheet({ open, onOpenChange, menu }: MenuItemsSheetProps) {
  const t = useTranslations("catalogMenus.items")

  const { data: items, isLoading } = useMenuItems(open ? menu.id : "")
  // The menu-item rows carry only product_id (backend menuItemResponse has no
  // product name), so the product list is what turns them into something a
  // human can read — and it is also the picker's source. Called without params
  // on purpose: GET /catalog/products ignores limit/offset today, and passing
  // them would only fork the query key away from the products page's cache.
  const { data: products, isLoading: productsLoading } = useProducts()
  const addItem = useAddMenuItem()
  const removeItem = useRemoveMenuItem()

  const [productId, setProductId] = useState("")
  const [priceOverride, setPriceOverride] = useState("")
  const [isActive, setIsActive] = useState(true)

  const productById = new Map<string, Product>((products ?? []).map((p) => [p.id, p]))
  const menuItems = items ?? []
  const placedIds = new Set(menuItems.map((i) => i.product_id))
  const availableProducts = (products ?? []).filter((p) => !placedIds.has(p.id))

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault()
    if (productId === "") {
      toast.error(t("productRequired"))
      return
    }
    // An empty override field means "use the product's own price" (null), not
    // "free" (0) — so only a non-empty field is parsed, and an unparseable
    // one is rejected instead of quietly falling back to null.
    let override: number | null = null
    if (priceOverride.trim() !== "") {
      override = parseLiraToKurus(priceOverride)
      if (override === null) {
        toast.error(t("priceInvalid"))
        return
      }
    }

    try {
      await addItem.mutateAsync({
        menuId: menu.id,
        product_id: productId,
        price_override: override,
        is_active: isActive,
      })
      toast.success(t("added"))
      setProductId("")
      setPriceOverride("")
      setIsActive(true)
    } catch {
      toast.error(t("addFailed"))
    }
  }

  async function handleRemove(targetProductId: string) {
    try {
      await removeItem.mutateAsync({ menuId: menu.id, productId: targetProductId })
      toast.success(t("removed"))
    } catch {
      toast.error(t("removeFailed"))
    }
  }

  // Editing an existing row reuses the add form: POST /menus/{id}/items is an
  // upsert on (menu_id, product_id), so "edit" is just the same call with the
  // row's current values pre-filled.
  function startEdit(targetProductId: string, currentOverride: number | null, active: boolean) {
    setProductId(targetProductId)
    setPriceOverride(currentOverride == null ? "" : formatKurusForInput(currentOverride))
    setIsActive(active)
  }

  const editingPlaced = placedIds.has(productId)

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full sm:max-w-xl">
        <SheetHeader>
          <SheetTitle>{t("title", { menu: menu.name })}</SheetTitle>
          <SheetDescription>{t("description")}</SheetDescription>
        </SheetHeader>

        <div className="space-y-6 overflow-y-auto px-4 pb-6">
          <form onSubmit={handleAdd} className="space-y-3 rounded-lg border p-4">
            <div className="space-y-2">
              <Label htmlFor="menu-item-product">{t("product")}</Label>
              <Select
                id="menu-item-product"
                value={productId}
                onValueChange={setProductId}
                disabled={productsLoading}
              >
                <SelectItem value="">{t("productPlaceholder")}</SelectItem>
                {/* The product being edited stays in the list even though it is
                    already on the menu — otherwise selecting it from the table
                    below would immediately blank the picker. */}
                {(editingPlaced
                  ? [...availableProducts, productById.get(productId)].filter(
                      (p): p is Product => p !== undefined,
                    )
                  : availableProducts
                ).map((product) => (
                  <SelectItem key={product.id} value={product.id}>
                    {product.name} — {formatKurus(product.price_amount)}
                  </SelectItem>
                ))}
              </Select>
              {!productsLoading && availableProducts.length === 0 && !editingPlaced && (
                <p className="text-xs text-muted-foreground">{t("noProductsLeft")}</p>
              )}
            </div>

            <div className="space-y-2">
              <Label htmlFor="menu-item-price">{t("priceOverride")}</Label>
              <Input
                id="menu-item-price"
                inputMode="decimal"
                placeholder={t("priceOverridePlaceholder")}
                value={priceOverride}
                onChange={(e) => setPriceOverride(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">{t("priceOverrideHint")}</p>
            </div>

            <div className="flex items-center gap-3">
              <Switch id="menu-item-active" checked={isActive} onCheckedChange={setIsActive} />
              <Label htmlFor="menu-item-active">{t("active")}</Label>
            </div>

            <Button type="submit" className="w-full" disabled={addItem.isPending}>
              {addItem.isPending ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" />}
              {editingPlaced ? t("update") : t("add")}
            </Button>
          </form>

          {isLoading ? (
            <div className="space-y-2">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-10 w-full" />
              ))}
            </div>
          ) : menuItems.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">{t("empty")}</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("columnProduct")}</TableHead>
                  <TableHead className="text-right">{t("columnPrice")}</TableHead>
                  <TableHead>{t("columnStatus")}</TableHead>
                  <TableHead className="w-[110px]" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {menuItems.map((item) => {
                  const product = productById.get(item.product_id)
                  return (
                    <TableRow key={item.product_id}>
                      <TableCell className="font-medium">
                        {/* A product deleted after being placed on the menu
                            still comes back in the item list; showing its id
                            beats showing an empty cell. */}
                        {product?.name ?? t("unknownProduct", { id: item.product_id.slice(0, 8) })}
                      </TableCell>
                      <TableCell className="text-right tabular-nums">
                        {item.price_override == null ? (
                          <span className="text-muted-foreground">
                            {product ? formatKurus(product.price_amount) : "—"}
                          </span>
                        ) : (
                          formatKurus(item.price_override)
                        )}
                      </TableCell>
                      <TableCell>
                        <Badge
                          variant="outline"
                          className={
                            item.is_active
                              ? "border-green-200 bg-green-100 text-green-700"
                              : "border-gray-200 bg-gray-100 text-gray-600"
                          }
                        >
                          {item.is_active ? t("statusActive") : t("statusPassive")}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <div className="flex justify-end gap-1">
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() =>
                              startEdit(item.product_id, item.price_override, item.is_active)
                            }
                          >
                            {t("edit")}
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-destructive hover:text-destructive"
                            disabled={removeItem.isPending}
                            aria-label={t("removeAria", {
                              product: product?.name ?? item.product_id.slice(0, 8),
                            })}
                            onClick={() => void handleRemove(item.product_id)}
                          >
                            <Trash2 className="size-3.5" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
