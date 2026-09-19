"use client"

import axios from "axios"
import { ArrowLeft, ListPlus, Loader2, Plus, Trash2 } from "lucide-react"
import { useTranslations } from "next-intl"
import Link from "next/link"
import { useState } from "react"
import { toast } from "sonner"

import { useBreadcrumbLabel } from "@/components/layouts/dynamic-breadcrumb"
import { FormDialog } from "@/components/layouts/form-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectItem } from "@/components/ui/select"
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
import {
  useAddMenuItem,
  useMenu,
  useMenuItems,
  useProducts,
  useRemoveMenuItem,
} from "@/hooks/use-catalog"
import { formatKurus, formatKurusForInput, parseLiraToKurus } from "@/lib/money"
import { productStatusVariant } from "@/lib/status-badge"
import type { Menu, MenuItem, Product } from "@/types"

// A malformed id (typo'd link, stale bookmark) renders the not-found state
// without firing the GET, same as the product editor.
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

type DialogState = { mode: "add" } | { mode: "edit"; item: MenuItem } | null

interface MenuItemsEditorProps {
  menuId: string
}

export function MenuItemsEditor({ menuId }: MenuItemsEditorProps) {
  const t = useTranslations("catalogMenus.items")

  const invalidId = !UUID_RE.test(menuId)
  const {
    data: menu,
    isLoading: menuLoading,
    isError: menuIsError,
    error: menuError,
  } = useMenu(invalidId ? "" : menuId)
  const notFound =
    invalidId || (menuIsError && axios.isAxiosError(menuError) && menuError.response?.status === 404)
  useBreadcrumbLabel(menu?.name)

  const { data: items, isLoading: itemsLoading } = useMenuItems(invalidId ? "" : menuId)
  // The menu-item rows carry only product_id (backend menuItemResponse has no
  // product name), so the product list is what turns them into something a
  // human can read — and it is also the picker's source. Called without params
  // on purpose: GET /catalog/products ignores limit/offset today, and passing
  // them would only fork the query key away from the products page's cache.
  const { data: products, isLoading: productsLoading } = useProducts()
  const removeItem = useRemoveMenuItem()

  const [dialog, setDialog] = useState<DialogState>(null)

  const productById = new Map<string, Product>((products ?? []).map((p) => [p.id, p]))
  const menuItems = items ?? []
  const placedIds = new Set(menuItems.map((i) => i.product_id))
  const availableProducts = (products ?? []).filter((p) => !placedIds.has(p.id))

  async function handleRemove(targetProductId: string) {
    try {
      await removeItem.mutateAsync({ menuId, productId: targetProductId })
      toast.success(t("removed"))
    } catch {
      toast.error(t("removeFailed"))
    }
  }

  if (notFound) {
    return (
      <div className="space-y-4">
        <BackLink />
        <p className="text-sm text-destructive">{t("notFound")}</p>
      </div>
    )
  }

  if (menuLoading || !menu) {
    return (
      <div className="space-y-6">
        <BackLink />
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-64 w-full" />
      </div>
    )
  }

  const isLoading = itemsLoading || productsLoading

  return (
    <div className="space-y-6">
      <div className="space-y-3">
        <BackLink />
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="space-y-1">
            <div className="flex items-center gap-3">
              <h1 className="text-2xl font-bold tracking-tight">{menu.name}</h1>
              <Badge variant={productStatusVariant(menu.is_active)}>
                {menu.is_active ? t("menuActive") : t("menuPassive")}
              </Badge>
            </div>
            <p className="text-sm text-muted-foreground">{menu.description || t("subtitle")}</p>
          </div>
          <Button type="button" onClick={() => setDialog({ mode: "add" })} disabled={productsLoading}>
            <Plus className="size-4" />
            {t("add")}
          </Button>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t("tableTitle")}</CardTitle>
          <CardDescription>{t("description")}</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-2">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-10 w-full" />
              ))}
            </div>
          ) : menuItems.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <ListPlus className="mb-4 size-12 text-muted-foreground" />
              <p className="text-sm text-muted-foreground">{t("empty")}</p>
              <Button type="button" className="mt-4" onClick={() => setDialog({ mode: "add" })}>
                <Plus className="size-4" />
                {t("addFirst")}
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("columnProduct")}</TableHead>
                  <TableHead className="text-right">{t("columnPrice")}</TableHead>
                  <TableHead>{t("columnStatus")}</TableHead>
                  <TableHead className="w-[140px]" />
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
                        <Badge variant={productStatusVariant(item.is_active)}>
                          {item.is_active ? t("statusActive") : t("statusPassive")}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <div className="flex justify-end gap-1">
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => setDialog({ mode: "edit", item })}
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
        </CardContent>
      </Card>

      {dialog && (
        // Keyed per target so the form's local state never leaks from one
        // item (or from add mode) into the next.
        <MenuItemFormDialog
          key={dialog.mode === "edit" ? dialog.item.product_id : "add"}
          menu={menu}
          item={dialog.mode === "edit" ? dialog.item : null}
          availableProducts={availableProducts}
          productById={productById}
          onClose={() => setDialog(null)}
        />
      )}
    </div>
  )
}

function BackLink() {
  const t = useTranslations("catalogMenus.items")
  return (
    <Link
      href="/catalog/menus"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
    >
      <ArrowLeft className="size-4" />
      {t("back")}
    </Link>
  )
}

interface MenuItemFormDialogProps {
  menu: Menu
  // Null means "add a new item"; a value means "edit this placed item".
  item: MenuItem | null
  availableProducts: Product[]
  productById: Map<string, Product>
  onClose: () => void
}

// POST /menus/{id}/items is an upsert on (menu_id, product_id), so editing a
// placed row is the same call as adding one, with the row's values pre-filled
// and the product locked.
function MenuItemFormDialog({
  menu,
  item,
  availableProducts,
  productById,
  onClose,
}: MenuItemFormDialogProps) {
  const t = useTranslations("catalogMenus.items")
  const addItem = useAddMenuItem()

  const editing = item !== null
  const [productId, setProductId] = useState(item?.product_id ?? "")
  const [priceOverride, setPriceOverride] = useState(
    item?.price_override == null ? "" : formatKurusForInput(item.price_override),
  )
  const [isActive, setIsActive] = useState(item?.is_active ?? true)

  const pickable = editing
    ? [productById.get(item.product_id)].filter((p): p is Product => p !== undefined)
    : availableProducts

  async function handleSubmit(e: React.FormEvent) {
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
      toast.success(editing ? t("updated") : t("added"))
      onClose()
    } catch {
      toast.error(editing ? t("updateFailed") : t("addFailed"))
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
      title={editing ? t("editTitle") : t("addTitle")}
      description={t("dialogDescription", { menu: menu.name })}
      busy={addItem.isPending}
      footer={
        <>
          <Button type="button" variant="outline" onClick={onClose} disabled={addItem.isPending}>
            {t("cancel")}
          </Button>
          <Button type="submit" form="menu-item-form" disabled={addItem.isPending}>
            {addItem.isPending ? <Loader2 className="size-4 animate-spin" /> : null}
            {editing ? t("update") : t("addSubmit")}
          </Button>
        </>
      }
    >
      <form id="menu-item-form" onSubmit={handleSubmit} className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="menu-item-product">{t("product")}</Label>
          <Select
            id="menu-item-product"
            value={productId}
            onValueChange={setProductId}
            disabled={editing}
          >
            <SelectItem value="">{t("productPlaceholder")}</SelectItem>
            {pickable.map((product) => (
              <SelectItem key={product.id} value={product.id}>
                {product.name} — {formatKurus(product.price_amount)}
              </SelectItem>
            ))}
          </Select>
          {!editing && availableProducts.length === 0 && (
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
      </form>
    </FormDialog>
  )
}
