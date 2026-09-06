"use client"

import { Plus, ShoppingBag } from "lucide-react"
import { useTranslations } from "next-intl"
import Link from "next/link"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { useMemo, useState } from "react"
import { toast } from "sonner"

import { ConfirmDialog } from "@/components/catalog/confirm-dialog"
import { ProductRowActions } from "@/components/catalog/product-row-actions"
import { toProductBody } from "@/components/catalog/product-editor"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Select, SelectItem } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  useCategories,
  useDeleteProduct,
  useCreateProduct,
  useModifierGroups,
  useProducts,
  useProductsModifierGroupIds,
  useUpdateProduct,
} from "@/hooks/use-catalog"
import { formatKurus } from "@/lib/money"
import { productStatusVariant } from "@/lib/status-badge"
import type { Product } from "@/types"

type StatusFilter = "all" | "active" | "inactive"

// Query keys the filter UI reads/writes. Kept in the URL (?q=&cat=&status=)
// so a bookmark or a page reload lands on the same filtered view — see the
// task brief's "filtre durumu URL query'sinde tutulur" requirement. The
// filters themselves live in local state (source of truth for rendering);
// the URL is a best-effort mirror written via router.replace, not read back
// reactively, so an in-app back/forward press does not resync the filters —
// acceptable for Faz 1, a reload or shared link still gets the right view.
export default function ProductsPage() {
  const t = useTranslations("catalog.products")
  const tCommon = useTranslations("catalog.common")
  const router = useRouter()
  const pathname = usePathname()
  const searchParams = useSearchParams()

  const [search, setSearch] = useState(() => searchParams.get("q") ?? "")
  const [categoryFilter, setCategoryFilter] = useState(() => searchParams.get("cat") ?? "all")
  const [statusFilter, setStatusFilter] = useState<StatusFilter>(
    () => (searchParams.get("status") as StatusFilter) ?? "all",
  )
  const [deleteTarget, setDeleteTarget] = useState<Product | null>(null)

  const productsQuery = useProducts()
  const { data: categoriesData } = useCategories()
  const { data: modifierGroupsData } = useModifierGroups()
  const updateProduct = useUpdateProduct()
  const deleteProduct = useDeleteProduct()
  const createProduct = useCreateProduct()

  const products = useMemo(() => productsQuery.data ?? [], [productsQuery.data])
  const categories = categoriesData ?? []
  const modifierGroupNameById = useMemo(
    () => new Map((modifierGroupsData ?? []).map((g) => [g.id, g.name])),
    [modifierGroupsData],
  )
  const productIds = useMemo(() => products.map((p) => p.id), [products])
  const groupIdsByProduct = useProductsModifierGroupIds(productIds)

  const syncQuery = (next: { q?: string; cat?: string; status?: StatusFilter }) => {
    const params = new URLSearchParams(searchParams.toString())
    const merged = {
      q: next.q ?? search,
      cat: next.cat ?? categoryFilter,
      status: next.status ?? statusFilter,
    }
    if (merged.q) params.set("q", merged.q)
    else params.delete("q")
    if (merged.cat && merged.cat !== "all") params.set("cat", merged.cat)
    else params.delete("cat")
    if (merged.status && merged.status !== "all") params.set("status", merged.status)
    else params.delete("status")
    const qs = params.toString()
    router.replace(qs ? `${pathname}?${qs}` : pathname)
  }

  const handleSearchChange = (value: string) => {
    setSearch(value)
    syncQuery({ q: value })
  }
  const handleCategoryChange = (value: string) => {
    setCategoryFilter(value)
    syncQuery({ cat: value })
  }
  const handleStatusChange = (value: StatusFilter) => {
    setStatusFilter(value)
    syncQuery({ status: value })
  }

  const uncategorizedCount = products.filter((p) => !p.category_id).length
  const activeCount = products.filter((p) => p.is_active).length

  const filteredProducts = useMemo(() => {
    const needle = search.trim().toLocaleLowerCase("tr")
    return products.filter((p) => {
      if (statusFilter === "active" && !p.is_active) return false
      if (statusFilter === "inactive" && p.is_active) return false
      if (categoryFilter === "none" && p.category_id) return false
      if (categoryFilter !== "all" && categoryFilter !== "none" && p.category_id !== categoryFilter) return false
      if (needle) {
        const haystack = `${p.name} ${p.description}`.toLocaleLowerCase("tr")
        if (!haystack.includes(needle)) return false
      }
      return true
    })
  }, [products, search, categoryFilter, statusFilter])

  const goToNew = () => router.push("/catalog/products/new")
  const goToDetail = (product: Product) => router.push(`/catalog/products/${product.id}`)

  const handleToggleActive = async (product: Product, isActive: boolean) => {
    try {
      await updateProduct.mutateAsync({ id: product.id, ...toProductBody(product, { is_active: isActive }) })
      toast.success(isActive ? t("toast.activated") : t("toast.deactivated"))
    } catch {
      toast.error(t("toast.error"))
    }
  }

  const handleDuplicate = async (product: Product) => {
    const { id: _id, tenant_id: _tenantId, created_at: _createdAt, updated_at: _updatedAt, ...rest } = product
    try {
      const res = await createProduct.mutateAsync({ ...rest, name: `${product.name} (kopya)` })
      toast.success(t("toast.created"))
      if (res.data?.id) router.push(`/catalog/products/${res.data.id}`)
    } catch {
      toast.error(t("toast.error"))
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    try {
      await deleteProduct.mutateAsync(deleteTarget.id)
      toast.success(t("toast.deleted"))
    } catch {
      toast.error(t("toast.error"))
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("title")}</h1>
          <p className="text-muted-foreground">
            {products.length > 0 ? t("count", { count: products.length, active: activeCount }) : t("subtitle")}
          </p>
        </div>
        <Button onClick={goToNew}>
          <Plus className="size-4" />
          {t("add")}
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <Input
          placeholder={t("search")}
          value={search}
          onChange={(e) => handleSearchChange(e.target.value)}
          className="max-w-xs"
        />
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            variant={categoryFilter === "all" ? "secondary" : "outline"}
            size="sm"
            aria-pressed={categoryFilter === "all"}
            onClick={() => handleCategoryChange("all")}
          >
            {t("filter.all")}
          </Button>
          {categories.map((category) => (
            <Button
              key={category.id}
              type="button"
              variant={categoryFilter === category.id ? "secondary" : "outline"}
              size="sm"
              aria-pressed={categoryFilter === category.id}
              onClick={() => handleCategoryChange(category.id)}
            >
              {category.name}
            </Button>
          ))}
          <Button
            type="button"
            variant={categoryFilter === "none" ? "secondary" : "outline"}
            size="sm"
            aria-pressed={categoryFilter === "none"}
            onClick={() => handleCategoryChange("none")}
          >
            {t("filter.uncategorized")} · {uncategorizedCount}
          </Button>
        </div>
        <Select
          value={statusFilter}
          onValueChange={(value) => handleStatusChange(value as StatusFilter)}
          className="w-auto"
          aria-label={t("columns.status")}
        >
          <SelectItem value="all">{t("filter.status.all")}</SelectItem>
          <SelectItem value="active">{t("filter.status.active")}</SelectItem>
          <SelectItem value="inactive">{t("filter.status.inactive")}</SelectItem>
        </Select>
      </div>

      <Card>
        <CardContent>
          {productsQuery.isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-12 w-full" />
              ))}
            </div>
          ) : productsQuery.isError ? (
            <div className="flex flex-col items-center justify-center gap-3 py-16 text-center">
              <p className="text-sm text-destructive">{tCommon("loadFailed")}</p>
              <Button variant="outline" size="sm" onClick={() => productsQuery.refetch()}>
                {tCommon("retry")}
              </Button>
            </div>
          ) : filteredProducts.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <ShoppingBag className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">{t("empty.title")}</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">{t("empty.body")}</p>
              <Button onClick={goToNew}>
                <Plus className="size-4" />
                {t("empty.cta")}
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("columns.name")}</TableHead>
                  <TableHead>{t("columns.category")}</TableHead>
                  <TableHead className="text-right">{t("columns.price")}</TableHead>
                  <TableHead>{t("columns.options")}</TableHead>
                  <TableHead>{t("columns.status")}</TableHead>
                  <TableHead className="w-[60px]" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {filteredProducts.map((product) => {
                  const category = categories.find((c) => c.id === product.category_id)
                  // Keyed by group id (not name) — two groups can share a
                  // name, and name is also just display data derived from
                  // the id, not a stable identity for the badge itself.
                  const groups = (groupIdsByProduct[product.id] ?? [])
                    .map((id) => ({ id, name: modifierGroupNameById.get(id) }))
                    .filter((g): g is { id: string; name: string } => Boolean(g.name))

                  return (
                    <TableRow
                      key={product.id}
                      className="cursor-pointer"
                      onClick={() => goToDetail(product)}
                    >
                      <TableCell>
                        <Link
                          href={`/catalog/products/${product.id}`}
                          className="font-medium hover:underline focus-visible:underline"
                          onClick={(e) => e.stopPropagation()}
                        >
                          {product.name}
                        </Link>
                        {product.description ? (
                          <div className="text-sm text-muted-foreground">{product.description}</div>
                        ) : null}
                      </TableCell>
                      <TableCell>
                        {category ? (
                          category.name
                        ) : (
                          <span className="text-status-warning-fg">{t("filter.uncategorized")}</span>
                        )}
                      </TableCell>
                      <TableCell className="text-right tabular-nums">
                        {formatKurus(product.price_amount)}
                      </TableCell>
                      <TableCell>
                        {groups.length > 0 ? (
                          <div className="flex flex-wrap gap-1">
                            {groups.map((group) => (
                              <Badge key={group.id} variant="outline">
                                {group.name}
                              </Badge>
                            ))}
                          </div>
                        ) : (
                          <span className="text-sm text-muted-foreground">{t("noOptions")}</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <Badge variant={productStatusVariant(product.is_active)}>
                          {product.is_active ? t("status.active") : t("status.inactive")}
                        </Badge>
                      </TableCell>
                      <TableCell onClick={(e) => e.stopPropagation()}>
                        <ProductRowActions
                          product={product}
                          onEdit={goToDetail}
                          onDuplicate={handleDuplicate}
                          onToggleActive={(p) => handleToggleActive(p, !p.is_active)}
                          onDeleteRequest={setDeleteTarget}
                        />
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
        title={t("deleteConfirm.title", { name: deleteTarget?.name ?? "" })}
        description={t("deleteConfirm.body")}
        confirmLabel={t("deleteConfirm.confirm")}
        cancelLabel={tCommon("cancel")}
        destructive
        onConfirm={handleDelete}
        secondaryAction={{
          label: t("deleteConfirm.deactivateInstead"),
          onClick: () => {
            if (deleteTarget) void handleToggleActive(deleteTarget, false)
          },
        }}
      />
    </div>
  )
}
