"use client"

import { useQueries } from "@tanstack/react-query"
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
import { Skeleton } from "@/components/ui/skeleton"
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
import { TAX_RATE_OPTIONS, UNIT_OPTIONS, type MenuItem } from "@/types"

const emptyValues: ProductFormValues = {
  name: "",
  categoryId: null,
  unit: UNIT_OPTIONS[0],
  description: "",
  priceKurus: null,
  taxRateBps: TAX_RATE_OPTIONS[0],
  sortOrder: 0,
  isActive: true,
}

interface ProductEditorProps {
  // Absent means "new product" — /catalog/products/new renders this with no
  // productId, /catalog/products/[id] passes params.id.
  productId?: string
}

export function ProductEditor({ productId }: ProductEditorProps) {
  const t = useTranslations("catalog.product")
  const tProducts = useTranslations("catalog.products")
  const router = useRouter()

  const isNew = !productId
  const { data: product, isLoading: productLoading } = useProduct(productId ?? "")
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

    const body = {
      name: values.name.trim(),
      category_id: values.categoryId,
      unit: values.unit,
      description: values.description,
      price_amount: values.priceKurus as number,
      tax_rate_bps: values.taxRateBps,
      sort_order: values.sortOrder,
      is_active: values.isActive,
    }

    try {
      if (isNew) {
        const created = await createProduct.mutateAsync({ ...body, currency: "TRY" })
        toast.success(tProducts("toast.created"))
        router.replace(`/catalog/products/${created.data.id}`)
      } else {
        await updateProduct.mutateAsync({ id: productId as string, ...body })
        toast.success(tProducts("toast.updated"))
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
    if (!productId) return
    try {
      await updateProduct.mutateAsync({ id: productId, is_active: false })
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

  const category = categories.find((c) => c.id === values.categoryId)
  // Design calls for "{n} seçenek grubu · {m} menüde" here too, but tr.json
  // has no key for either composed count phrase — rather than inventing
  // Turkish copy inline, those two segments are omitted; see task-4 report.
  const summaryParts = [
    category?.name,
    values.priceKurus != null ? formatKurus(values.priceKurus) : null,
    `%${values.taxRateBps / 100} KDV`,
  ].filter((part): part is string => Boolean(part))

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
                    ? "border-green-200 bg-green-100 text-green-700"
                    : "border-gray-200 bg-gray-100 text-gray-600"
                }
              >
                {values.isActive ? tProducts("status.active") : tProducts("status.inactive")}
              </Badge>
            ) : null}
          </div>
          {!isNew && summaryParts.length > 0 ? (
            <p className="text-sm text-muted-foreground">{summaryParts.join(" · ")}</p>
          ) : null}
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
        {!isNew && productId ? (
          <div className="space-y-6 lg:col-span-5">
            <ProductModifierGroupsCard productId={productId} assignedGroupIds={assignedGroupIds} />
            <ProductMenusCard productId={productId} menus={menus} menuIdsWithProduct={menuIdsWithProduct} />
          </div>
        ) : null}
      </div>

      {!isNew && productId ? (
        <ConfirmDialog
          open={deleteOpen}
          onOpenChange={setDeleteOpen}
          title={tProducts("deleteConfirm.title", { name: values.name })}
          description={tProducts("deleteConfirm.body")}
          confirmLabel={tProducts("deleteConfirm.confirm")}
          destructive
          onConfirm={handleDelete}
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
