"use client"

import { Store } from "lucide-react"
import { useTranslations } from "next-intl"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { type ReactNode, useMemo, useState } from "react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
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
  type BranchOverrideInput,
  useBranchOverrides,
  useDeleteBranchOverride,
  useUpsertBranchOverride,
} from "@/hooks/use-branch-overrides"
import { useCategories, useProducts } from "@/hooks/use-catalog"
import { useBranches } from "@/hooks/use-tenant"
import { branchSaleState, overrideDiffers } from "@/lib/branch-overrides"
import { formatKurus, formatKurusForInput, parseLiraToKurus } from "@/lib/money"
import { branchSaleStateVariant } from "@/lib/status-badge"
import { useAuthStore } from "@/store/auth-store"
import type { BranchProductOverride, Product } from "@/types"

interface RowProps {
  product: Product
  categoryName: string | null
  override: BranchProductOverride | undefined
  onPriceCommit: (product: Product, priceAmount: number | null, onFailure: () => void) => Promise<void>
  onAvailabilityChange: (product: Product, isAvailable: boolean) => Promise<void>
  onReset: (product: Product) => Promise<void>
}

// The input is uncontrolled and re-keyed by the stored price: whatever the
// server (or an optimistic write / rollback) says the price is, the field
// remounts showing it, while the owner's half-typed text is never overwritten
// by a re-render. `revision` covers the one case the price key cannot: a save
// that fails before React ever rendered the optimistic price, so the stored
// price (and key) never changed and the DOM would keep the rejected text.
// Text only becomes a request on blur/Enter, and only if it parses — an
// unparseable value must never be sent as "clear the price".
function BranchPricingRow({
  product,
  categoryName,
  override,
  onPriceCommit,
  onAvailabilityChange,
  onReset,
}: RowProps) {
  const t = useTranslations("catalog.branchPricing")
  const [invalid, setInvalid] = useState(false)
  const [revision, setRevision] = useState(0)

  const storedPrice = override?.price_amount ?? null
  const isAvailable = override?.is_available ?? true
  const state = branchSaleState(override)
  const differs = overrideDiffers(override)

  const commit = (input: HTMLInputElement) => {
    const text = input.value.trim()
    const parsed = text === "" ? null : parseLiraToKurus(text)
    if (text !== "" && parsed === null) {
      input.value = storedPrice === null ? "" : formatKurusForInput(storedPrice)
      setInvalid(false)
      toast.error(t("toast.invalidPrice"))
      return
    }
    setInvalid(false)
    if (parsed === storedPrice) {
      input.value = storedPrice === null ? "" : formatKurusForInput(storedPrice)
      return
    }
    onPriceCommit(product, parsed, () => setRevision((r) => r + 1))
  }

  return (
    <TableRow data-testid={`branch-pricing-row-${product.id}`}>
      <TableCell>
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium">{product.name}</span>
          {!product.is_active ? <Badge variant="outline">{t("productInactive")}</Badge> : null}
        </div>
      </TableCell>
      <TableCell>
        {categoryName ?? <span className="text-status-warning-fg">{t("uncategorized")}</span>}
      </TableCell>
      <TableCell className="text-right tabular-nums">{formatKurus(product.price_amount)}</TableCell>
      <TableCell className="w-40">
        <Input
          key={`${storedPrice ?? "none"}:${revision}`}
          defaultValue={storedPrice === null ? "" : formatKurusForInput(storedPrice)}
          inputMode="decimal"
          placeholder={formatKurusForInput(product.price_amount)}
          title={storedPrice === null ? t("priceHint") : undefined}
          aria-label={t("priceLabel", { name: product.name })}
          aria-invalid={invalid || undefined}
          className="text-right tabular-nums"
          onChange={(e) => {
            const text = e.target.value.trim()
            setInvalid(text !== "" && parseLiraToKurus(text) === null)
          }}
          onBlur={(e) => commit(e.currentTarget)}
          onKeyDown={(e) => {
            if (e.key === "Enter") e.currentTarget.blur()
          }}
        />
      </TableCell>
      <TableCell>
        <Switch
          checked={isAvailable}
          aria-label={t("availableLabel", { name: product.name })}
          onCheckedChange={(checked) => onAvailabilityChange(product, checked)}
        />
      </TableCell>
      <TableCell>
        <Badge variant={branchSaleStateVariant(state)}>{t(`state.${state}`)}</Badge>
      </TableCell>
      <TableCell className="text-right">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={!differs}
          aria-label={t("resetLabel", { name: product.name })}
          onClick={() => onReset(product)}
        >
          {t("reset")}
        </Button>
      </TableCell>
    </TableRow>
  )
}

// Owner-facing screen for ADR-DATA-009: pick a branch, then edit each
// product's branch price and whether that branch sells it. The list is the
// TENANT catalog joined client-side with the branch's overrides — never
// GET /products?branch_id=, which drops closed products and would leave the
// owner no row to re-open them from.
export function BranchPricing() {
  const t = useTranslations("catalog.branchPricing")
  const tCommon = useTranslations("catalog.common")
  const router = useRouter()
  const pathname = usePathname()
  const searchParams = useSearchParams()
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""

  const [branchParam, setBranchParam] = useState(() => searchParams.get("branch") ?? "")
  const [search, setSearch] = useState(() => searchParams.get("q") ?? "")
  const [onlyOverridden, setOnlyOverridden] = useState(() => searchParams.get("only") === "1")

  const branchesQuery = useBranches(tenantId)
  const branches = useMemo(() => branchesQuery.data ?? [], [branchesQuery.data])
  const branchId = branches.some((b) => b.id === branchParam) ? branchParam : (branches[0]?.id ?? "")

  const productsQuery = useProducts()
  const categoriesQuery = useCategories()
  const overridesQuery = useBranchOverrides(branchId)
  const upsert = useUpsertBranchOverride(branchId)
  const remove = useDeleteBranchOverride(branchId)

  const products = useMemo(() => productsQuery.data ?? [], [productsQuery.data])
  const categoryNameById = useMemo(
    () => new Map((categoriesQuery.data ?? []).map((c) => [c.id, c.name])),
    [categoriesQuery.data],
  )
  const overrideByProduct = useMemo(
    () => new Map((overridesQuery.data ?? []).map((row) => [row.product_id, row])),
    [overridesQuery.data],
  )

  const overriddenCount = useMemo(
    () => products.filter((p) => overrideDiffers(overrideByProduct.get(p.id))).length,
    [products, overrideByProduct],
  )

  const visibleProducts = useMemo(() => {
    const needle = search.trim().toLocaleLowerCase("tr")
    return products.filter((p) => {
      if (onlyOverridden && !overrideDiffers(overrideByProduct.get(p.id))) return false
      if (needle && !p.name.toLocaleLowerCase("tr").includes(needle)) return false
      return true
    })
  }, [products, overrideByProduct, search, onlyOverridden])

  // Best-effort mirror of the filters into the URL (same trade-off as the
  // products list): a reload or a shared link lands on the same view.
  const syncQuery = (next: { branch?: string; q?: string; only?: boolean }) => {
    const params = new URLSearchParams(searchParams.toString())
    const merged = {
      branch: next.branch ?? branchId,
      q: next.q ?? search,
      only: next.only ?? onlyOverridden,
    }
    if (merged.branch) params.set("branch", merged.branch)
    else params.delete("branch")
    if (merged.q) params.set("q", merged.q)
    else params.delete("q")
    if (merged.only) params.set("only", "1")
    else params.delete("only")
    const qs = params.toString()
    router.replace(qs ? `${pathname}?${qs}` : pathname)
  }

  const currentOverride = (productId: string) => overrideByProduct.get(productId)

  // mutateAsync rather than mutate(vars, { onError }): per-call callbacks of
  // mutate() only fire for the observer's most recent call, so a failed save
  // followed by another quick edit would lose its error toast.
  const save = async (input: BranchOverrideInput, onFailure?: () => void) => {
    try {
      await upsert.mutateAsync(input)
    } catch {
      toast.error(t("toast.error"))
      onFailure?.()
    }
  }

  const handlePriceCommit = (product: Product, priceAmount: number | null, onFailure: () => void) =>
    save(
      {
        productId: product.id,
        is_available: currentOverride(product.id)?.is_available ?? true,
        price_amount: priceAmount,
      },
      onFailure,
    )

  const handleAvailabilityChange = (product: Product, isAvailable: boolean) =>
    save({
      productId: product.id,
      is_available: isAvailable,
      price_amount: currentOverride(product.id)?.price_amount ?? null,
    })

  const handleReset = async (product: Product) => {
    try {
      await remove.mutateAsync({ productId: product.id })
    } catch {
      toast.error(t("toast.error"))
    }
  }

  const isLoading = branchesQuery.isLoading || productsQuery.isLoading || (branchId !== "" && overridesQuery.isLoading)
  const isError = branchesQuery.isError || productsQuery.isError || overridesQuery.isError
  const retry = () => {
    void branchesQuery.refetch()
    void productsQuery.refetch()
    void overridesQuery.refetch()
  }

  let body: ReactNode
  if (isLoading) {
    body = (
      <div className="space-y-3">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    )
  } else if (isError) {
    body = (
      <div className="flex flex-col items-center justify-center gap-3 py-16 text-center">
        <p className="text-sm text-destructive">{tCommon("loadFailed")}</p>
        <Button variant="outline" size="sm" onClick={retry}>
          {tCommon("retry")}
        </Button>
      </div>
    )
  } else if (branches.length === 0 || visibleProducts.length === 0) {
    const message =
      branches.length === 0
        ? t("empty.noBranches")
        : products.length === 0
          ? t("empty.noProducts")
          : search.trim() !== ""
            ? t("empty.noMatch")
            : t("empty.allTenant")
    body = (
      <div className="flex flex-col items-center justify-center py-16 text-center">
        <Store className="mb-4 size-12 text-muted-foreground" />
        <p className="text-sm text-muted-foreground">{message}</p>
      </div>
    )
  } else {
    body = (
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("columns.product")}</TableHead>
            <TableHead>{t("columns.category")}</TableHead>
            <TableHead className="text-right">{t("columns.tenantPrice")}</TableHead>
            <TableHead className="text-right">{t("columns.branchPrice")}</TableHead>
            <TableHead>{t("columns.available")}</TableHead>
            <TableHead>{t("columns.state")}</TableHead>
            <TableHead className="w-[140px]" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {visibleProducts.map((product) => (
            <BranchPricingRow
              key={product.id}
              product={product}
              categoryName={product.category_id ? (categoryNameById.get(product.category_id) ?? null) : null}
              override={overrideByProduct.get(product.id)}
              onPriceCommit={handlePriceCommit}
              onAvailabilityChange={handleAvailabilityChange}
              onReset={handleReset}
            />
          ))}
        </TableBody>
      </Table>
    )
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">{t("title")}</h1>
        <p className="text-muted-foreground">
          {products.length > 0 && branchId !== ""
            ? t("count", { count: products.length, overridden: overriddenCount })
            : t("subtitle")}
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <Select
          value={branchId}
          onValueChange={(value) => {
            setBranchParam(value)
            syncQuery({ branch: value })
          }}
          className="w-auto min-w-48"
          aria-label={t("branch")}
        >
          {branches.map((branch) => (
            <SelectItem key={branch.id} value={branch.id}>
              {branch.is_active ? branch.name : t("branchInactive", { name: branch.name })}
            </SelectItem>
          ))}
        </Select>
        <Input
          placeholder={t("search")}
          aria-label={t("search")}
          value={search}
          onChange={(e) => {
            setSearch(e.target.value)
            syncQuery({ q: e.target.value })
          }}
          className="max-w-xs"
        />
        <Button
          type="button"
          variant={onlyOverridden ? "secondary" : "outline"}
          size="sm"
          aria-pressed={onlyOverridden}
          onClick={() => {
            setOnlyOverridden(!onlyOverridden)
            syncQuery({ only: !onlyOverridden })
          }}
        >
          {t("onlyOverridden")}
        </Button>
      </div>

      <Card>
        <CardContent>{body}</CardContent>
      </Card>
    </div>
  )
}
