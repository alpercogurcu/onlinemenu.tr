"use client"

import { useTranslations } from "next-intl"

import { MoneyInput } from "@/components/catalog/money-input"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectItem } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { formatKurus } from "@/lib/money"
import { TAX_RATE_OPTIONS, UNIT_OPTIONS, type Category } from "@/types"

export interface ProductFormValues {
  name: string
  categoryId: string | null
  unit: string
  description: string
  // Kuruş (int64) — see components/catalog/money-input.tsx.
  priceKurus: number | null
  taxRateBps: number
  sortOrder: number
  isActive: boolean
}

export interface ProductFormErrors {
  name?: string
  price?: string
}

interface ProductFormProps {
  values: ProductFormValues
  errors: ProductFormErrors
  categories: Category[]
  onChange: (patch: Partial<ProductFormValues>) => void
}

// tax = round(gross * bps / (10000 + bps)); base = gross - tax — the same
// gross-up formula the price/tax hint and the summary line in
// product-editor.tsx both rely on, kept in one place so they can't drift.
export function computeTax(priceKurus: number | null, taxRateBps: number): { base: number | null; tax: number | null } {
  if (priceKurus == null) return { base: null, tax: null }
  const tax = Math.round((priceKurus * taxRateBps) / (10000 + taxRateBps))
  return { base: priceKurus - tax, tax }
}

export function ProductForm({ values, errors, categories, onChange }: ProductFormProps) {
  const t = useTranslations("catalog.product")

  const { base, tax } = computeTax(values.priceKurus, values.taxRateBps)

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>{t("sections.basics")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="product-name">
              {t("fields.name")}
              {" *"}
            </Label>
            <Input
              id="product-name"
              value={values.name}
              aria-invalid={Boolean(errors.name)}
              aria-describedby={errors.name ? "product-name-error" : undefined}
              onChange={(e) => onChange({ name: e.target.value })}
            />
            {errors.name ? (
              <p id="product-name-error" className="text-sm text-destructive">
                {errors.name}
              </p>
            ) : null}
          </div>

          <div className="space-y-2">
            <Label htmlFor="product-category">{t("fields.category")}</Label>
            <Select
              id="product-category"
              value={values.categoryId ?? ""}
              onValueChange={(value) => onChange({ categoryId: value === "" ? null : value })}
            >
              <SelectItem value="">—</SelectItem>
              {categories.map((category) => (
                <SelectItem key={category.id} value={category.id}>
                  {category.name}
                </SelectItem>
              ))}
            </Select>
          </div>

          <div className="space-y-2">
            <Label htmlFor="product-unit">{t("fields.unit")}</Label>
            <Select
              id="product-unit"
              value={values.unit}
              onValueChange={(value) => onChange({ unit: value })}
            >
              {UNIT_OPTIONS.map((unit) => (
                <SelectItem key={unit} value={unit}>
                  {unit}
                </SelectItem>
              ))}
            </Select>
          </div>

          <div className="space-y-2">
            <Label htmlFor="product-description">{t("fields.description")}</Label>
            <Textarea
              id="product-description"
              value={values.description}
              onChange={(e) => onChange({ description: e.target.value })}
            />
            <p className="text-xs text-muted-foreground">{t("fields.descriptionHint")}</p>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t("sections.pricing")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="product-price">
              {t("fields.price")}
              {" *"}
            </Label>
            <MoneyInput
              id="product-price"
              valueKurus={values.priceKurus}
              onChangeKurus={(value) => onChange({ priceKurus: value })}
            />
            {errors.price ? (
              <p className="text-sm text-destructive">{errors.price}</p>
            ) : (
              <p className="text-xs text-muted-foreground">{t("fields.priceHint")}</p>
            )}
          </div>

          <div className="space-y-2">
            <Label htmlFor="product-tax">{t("fields.taxRate")}</Label>
            <Select
              id="product-tax"
              value={String(values.taxRateBps)}
              onValueChange={(value) => onChange({ taxRateBps: Number(value) })}
            >
              {TAX_RATE_OPTIONS.map((bps) => (
                <SelectItem key={bps} value={String(bps)}>
                  %{bps / 100}
                </SelectItem>
              ))}
            </Select>
            <p className="text-xs text-muted-foreground">
              {t("fields.taxHint", { base: formatKurus(base), tax: formatKurus(tax) })}
            </p>
          </div>

          <div className="space-y-2">
            <Label htmlFor="product-sort-order">{t("fields.sortOrder")}</Label>
            <Input
              id="product-sort-order"
              type="number"
              value={values.sortOrder}
              onChange={(e) => onChange({ sortOrder: Number(e.target.value) || 0 })}
            />
            <p className="text-xs text-muted-foreground">{t("fields.sortOrderHint")}</p>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t("sections.sale")}</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex items-start gap-3">
            <Switch
              id="product-active"
              checked={values.isActive}
              onCheckedChange={(checked) => onChange({ isActive: checked })}
            />
            <div className="space-y-1">
              <Label htmlFor="product-active">{t("fields.active")}</Label>
              <p className="text-xs text-muted-foreground">{t("fields.activeHint")}</p>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
