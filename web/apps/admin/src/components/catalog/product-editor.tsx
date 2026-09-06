"use client"

import { useQueries } from "@tanstack/react-query"
import axios from "axios"
import { useTranslations } from "next-intl"
import { useRouter } from "next/navigation"
import { useEffect, useState } from "react"
import { toast } from "sonner"

import { ConfirmDialog } from "@/components/catalog/confirm-dialog"
import {
  ProductForm,
  type ProductFormErrors,
  type ProductFormValues,
} from "@/components/catalog/product-form"
import { ProductMenusCard } from "@/components/catalog/product-menus-card"
import { ProductModifierGroupsCard } from "@/components/catalog/product-modifier-groups-card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useBreadcrumbLabel } from "@/components/layouts/dynamic-breadcrumb"
import {
  useCategories,
  useCreateProduct,
  useDeleteProduct,
  useMenus,
  useProduct,
  useProductModifierGroupIds,
  useUpdateProduct,
} from "@/hooks/use-catalog"
import api from "@/lib/api"
import { formatKurus } from "@/lib/money"
import { TAX_RATE_OPTIONS, UNIT_OPTIONS, type MenuItem, type Product } from "@/types"

// Route params are free-form strings — a stray/malformed /catalog/products/{id}
// (typo'd link, stale bookmark) should render the not-found state below
// immediately, without even firing the GET, rather than a raw 4xx from the
// backend or an empty form pretending to be a real product.
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

const emptyValues: ProductFormValues = {
  name: "",
  categoryId: null,
  unit: UNIT_OPTIONS[0],
  description: "",
  priceKurus: null,
  // Defaults to %10 rather than TAX_RATE_OPTIONS[0] (%0) — %10 is the common
  // case for new products and %0 is easy to leave unnoticed.
  taxRateBps: 1000,
  sortOrder: 0,
  isActive: true,
}

// Backend PUT /catalog/products/{id} REPLACES the whole row from these ten
// fields — a body missing any of them zeroes it out server-side. Every PUT
// call (here and in the products list page) must go through this so a
// single-field action like "deactivate" can never regress into a partial
// body again.
export function toProductBody(
  product: Product,
  overrides: Partial<
    Pick<
      Product,
      | "category_id"
      | "name"
      | "description"
      | "price_amount"
      | "currency"
      | "unit"
      | "tax_rate_bps"
      | "is_active"
      | "sort_order"
      | "source_stock_item_id"
    >
  > = {},
) {
  return {
    category_id: product.category_id,
    name: product.name,
    description: product.description,
    price_amount: product.price_amount,
    currency: product.currency,
    unit: product.unit,
    tax_rate_bps: product.tax_rate_bps,
    is_active: product.is_active,
    sort_order: product.sort_order,
    // omitempty on the wire — undefined (never assigned) and null (explicitly
    // cleared) both mean "no stock link", so both fold to null here.
    source_stock_item_id: product.source_stock_item_id ?? null,
    ...overrides,
  }
}

interface ProductEditorProps {
  // Absent means "new product" — /catalog/products/new renders this with no
  // productId, /catalog/products/[id] passes params.id.
  productId?: string
}

export function ProductEditor({ productId }: ProductEditorProps) {
  const t = useTranslations("catalog.product")
  const tProducts = useTranslations("catalog.products")
  const tCommon = useTranslations("catalog.common")
  const router = useRouter()

  const isNew = !productId
  // A malformed id skips the network call entirely (useProduct("") is
  // disabled) — notFound below then fires on this instead of waiting for a
  // response.
  const invalidId = !isNew && productId !== undefined && !UUID_RE.test(productId)
  const {
    data: product,
    isLoading: productLoading,
    isError: productIsError,
    error: productError,
  } = useProduct(invalidId ? "" : (productId ?? ""))
  const notFound =
    !isNew &&
    (invalidId || (productIsError && axios.isAxiosError(productError) && productError.response?.status === 404))
  // Lets the shared breadcrumb show the product's own name ("Adana Kebap")
  // as the last crumb instead of the raw UUID route param.
  useBreadcrumbLabel(product?.name)
  const { data: categoriesData } = useCategories()
  const categories = categoriesData ?? []
  const { data: menusData } = useMenus()
  const menus = menusData ?? []
  const { data: assignedGroupIdsData } = useProductModifierGroupIds(productId ?? "")
  const assignedGroupIds = assignedGroupIdsData ?? []

  // Batched "which menus contain this product" lookup — one request per
  // menu, sharing useMenuItems' own query key (["menus", id, "items"]) so
  // both read/write the same cache entry. Same shape as
  // useProductsModifierGroupIds in use-catalog.ts, kept local here because
  // it is only needed by this editor (menu list + summary line), not
  // exported for reuse elsewhere.
  const menuItemsResults = useQueries({
    queries: menus.map((menu) => ({
      queryKey: ["menus", menu.id, "items"] as const,
      queryFn: async () => {
        const { data } = await api.get<MenuItem[]>(`/api/v1/catalog/menus/${menu.id}/items`)
        return data ?? []
      },
      enabled: Boolean(menu.id) && !isNew,
    })),
  })
  const menuIdsWithProduct = new Set<string>()
  menus.forEach((menu, i) => {
    const items = menuItemsResults[i]?.data ?? []
    if (items.some((item) => item.product_id === productId)) menuIdsWithProduct.add(menu.id)
  })

  const createProduct = useCreateProduct()
  const updateProduct = useUpdateProduct()
  const deleteProduct = useDeleteProduct()

  const [values, setValues] = useState<ProductFormValues>(emptyValues)
  // Snapshot taken the moment `product` is (re)seeded — compared against
  // `values`, not against `product` itself, so the dirty check isn't tripped
  // by money/select coercions that don't round-trip identically through the
  // form's own value shape.
  const [baseline, setBaseline] = useState<ProductFormValues>(emptyValues)
  const [errors, setErrors] = useState<ProductFormErrors>({})
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [unsavedOpen, setUnsavedOpen] = useState(false)

  // Re-seed form state whenever the loaded product changes — same
  // trade-off (and the same react-hooks/set-state-in-effect warning) as
  // modifier-options-editor.tsx's identical resync effect: reading/writing
  // a ref during render (the lint-clean alternative) is itself a hard error
  // under this project's react-hooks/refs rule, so the effect is the lesser
  // evil. `baseline` is snapshotted here too, at the same moment, so the
  // dirty check below compares against what the form actually renders
  // rather than re-deriving it from `product` a second time.
  useEffect(() => {
    if (!product) return
    const seeded: ProductFormValues = {
      name: product.name,
      categoryId: product.category_id,
      unit: product.unit,
      description: product.description,
      priceKurus: product.price_amount,
      taxRateBps: product.tax_rate_bps,
      sortOrder: product.sort_order,
      isActive: product.is_active,
    }
    setValues(seeded)
    setBaseline(seeded)
  }, [product])

  const isDirty = JSON.stringify(values) !== JSON.stringify(baseline)

  useEffect(() => {
    if (!isDirty) return
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      e.returnValue = ""
    }
    window.addEventListener("beforeunload", handler)
    return () => window.removeEventListener("beforeunload", handler)
  }, [isDirty])

  function handleChange(patch: Partial<ProductFormValues>) {
    setValues((v) => ({ ...v, ...patch }))
  }

  function validate(): ProductFormErrors {
    const next: ProductFormErrors = {}
    if (!values.name.trim()) next.name = t("validation.nameRequired")
    if (values.priceKurus == null) next.price = t("validation.priceRequired")
    return next
  }

  const isSaving = createProduct.isPending || updateProduct.isPending

  async function handleSave() {
    const nextErrors = validate()
    setErrors(nextErrors)
    if (Object.keys(nextErrors).length > 0) return

    // currency and source_stock_item_id aren't form-editable fields — the
    // form only ever changes the eight ProductFormValues above — so they're
    // carried through from the loaded product rather than from `values`.
    // `product?.` (not `product!`) because on a non-404 GET failure
    // `notFound` stays false and `productLoading` settles false too, so the
    // form can render with `product` still `undefined`.
    const body = {
      name: values.name.trim(),
      category_id: values.categoryId,
      unit: values.unit,
      description: values.description,
      price_amount: values.priceKurus as number,
      currency: product?.currency ?? "TRY",
      tax_rate_bps: values.taxRateBps,
      sort_order: values.sortOrder,
      is_active: values.isActive,
      source_stock_item_id: product?.source_stock_item_id ?? null,
    }

    try {
      if (isNew) {
        const created = await createProduct.mutateAsync({ ...body, currency: "TRY" })
        toast.success(tProducts("toast.created"))
        router.replace(`/catalog/products/${created.data.id}`)
      } else {
        await updateProduct.mutateAsync({ id: productId as string, ...body })
        toast.success(tProducts("toast.updated"))
        // Reset the dirty baseline from what was actually just saved, right
        // away — waiting for the refetch to re-seed it would leave isDirty
        // true (and "Vazgeç" popping the unsaved-changes dialog) for however
        // long that request takes.
        const saved: ProductFormValues = {
          name: body.name,
          categoryId: body.category_id,
          unit: body.unit,
          description: body.description,
          priceKurus: body.price_amount,
          taxRateBps: body.tax_rate_bps,
          sortOrder: body.sort_order,
          isActive: body.is_active,
        }
        setValues(saved)
        setBaseline(saved)
      }
    } catch {
      toast.error(tProducts("toast.error"))
    }
  }

  function handleCancel() {
    if (isDirty) {
      setUnsavedOpen(true)
      return
    }
    router.push("/catalog/products")
  }

  async function handleDelete() {
    if (!productId) return
    await deleteProduct.mutateAsync(productId)
    toast.success(tProducts("toast.deleted"))
    router.push("/catalog/products")
  }

  async function handleDeactivate() {
    if (!productId || !product) return
    try {
      await updateProduct.mutateAsync({ id: productId, ...toProductBody(product, { is_active: false }) })
      toast.success(tProducts("toast.deactivated"))
    } catch {
      toast.error(tProducts("toast.error"))
    }
  }

  if (!isNew && productLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-64 w-full" />
      </div>
    )
  }

  if (notFound) {
    return (
      <div className="space-y-4">
        <p className="text-sm text-destructive">{t("notFound")}</p>
        <Button type="button" variant="outline" onClick={() => router.push("/catalog/products")}>
          {t("cancel")}
        </Button>
      </div>
    )
  }

  const category = categories.find((c) => c.id === values.categoryId)
  // catalog.product.summary composes the whole line in one ICU string —
  // category/price/rate come straight from the form's own values, groups and
  // menus counts are the same assignedGroupIds/menuIdsWithProduct this
  // component already fetches for the two cards below (no lifting needed,
  // both live in this scope).
  const summary = t("summary", {
    category: category?.name ?? tProducts("filter.uncategorized"),
    price: values.priceKurus != null ? formatKurus(values.priceKurus) : "—",
    rate: values.taxRateBps / 100,
    groups: assignedGroupIds.length,
    menus: menuIdsWithProduct.size,
  })

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold tracking-tight">{isNew ? t("new") : values.name}</h1>
            {!isNew ? (
              <Badge
                variant="outline"
                className={
                  values.isActive
                    ? "bg-status-success-bg text-status-success-fg border-status-success-border"
                    : "bg-status-neutral-bg text-status-neutral-fg border-status-neutral-border"
                }
              >
                {values.isActive ? tProducts("status.active") : tProducts("status.inactive")}
              </Badge>
            ) : null}
          </div>
          {!isNew ? <p className="text-sm text-muted-foreground">{summary}</p> : null}
        </div>
        <div className="flex items-center gap-2">
          {!isNew ? (
            <Button
              type="button"
              variant="ghost"
              className="text-destructive hover:text-destructive"
              onClick={() => setDeleteOpen(true)}
            >
              {t("delete")}
            </Button>
          ) : null}
          <Button type="button" variant="outline" onClick={handleCancel}>
            {t("cancel")}
          </Button>
          <Button type="button" onClick={() => void handleSave()} disabled={isSaving}>
            {t("save")}
          </Button>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-12">
        <div className="lg:col-span-7">
          <ProductForm values={values} errors={errors} categories={categories} onChange={handleChange} />
        </div>
        <div className="space-y-6 lg:col-span-5">
          {!isNew && productId ? (
            <>
              <ProductModifierGroupsCard productId={productId} assignedGroupIds={assignedGroupIds} />
              <ProductMenusCard productId={productId} menus={menus} menuIdsWithProduct={menuIdsWithProduct} />
            </>
          ) : (
            <>
              <Card>
                <CardHeader>
                  <CardTitle>{t("groups.title")}</CardTitle>
                </CardHeader>
                <CardContent>
                  <p className="text-sm text-muted-foreground">{t("groups.saveFirst")}</p>
                </CardContent>
              </Card>
              <Card>
                <CardHeader>
                  <CardTitle>{t("menus.title")}</CardTitle>
                </CardHeader>
                <CardContent>
                  <p className="text-sm text-muted-foreground">{t("menus.saveFirst")}</p>
                </CardContent>
              </Card>
            </>
          )}
        </div>
      </div>

      {!isNew && productId ? (
        <ConfirmDialog
          open={deleteOpen}
          onOpenChange={setDeleteOpen}
          title={tProducts("deleteConfirm.title", { name: values.name })}
          description={tProducts("deleteConfirm.body")}
          confirmLabel={tProducts("deleteConfirm.confirm")}
          cancelLabel={tCommon("cancel")}
          destructive
          onConfirm={handleDelete}
          onError={() => toast.error(tProducts("toast.error"))}
          secondaryAction={{
            label: tProducts("deleteConfirm.deactivateInstead"),
            onClick: () => void handleDeactivate(),
          }}
        />
      ) : null}

      <ConfirmDialog
        open={unsavedOpen}
        onOpenChange={setUnsavedOpen}
        title={t("unsavedTitle")}
        description={t("unsavedBody")}
        confirmLabel={t("unsavedLeave")}
        cancelLabel={t("unsavedStay")}
        onConfirm={() => router.push("/catalog/products")}
      />
    </div>
  )
}
