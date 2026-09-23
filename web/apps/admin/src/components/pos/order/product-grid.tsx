"use client"

import { Loader2, SlidersHorizontal } from "lucide-react"
import { useTranslations } from "next-intl"

import { formatMoney } from "@onlinemenu/pos-core"

import { cn } from "@/lib/utils"
import type { Category, Product } from "@/types"

interface CategoryChipsProps {
  categories: Category[]
  activeId: string
  onSelect: (id: string) => void
}

/**
 * Fixed-order category chips, sticky under the page header so the waiter can
 * switch category from anywhere in a long list. Horizontal scroll with a fade
 * on the right edge as the "there is more" hint (spec bulgu #15).
 */
export function CategoryChips({ categories, activeId, onSelect }: CategoryChipsProps) {
  const t = useTranslations("posOrder")
  return (
    <div className="bg-background sticky top-0 z-10 -mx-4 border-b px-4 py-2">
      <div className="relative">
        <div
          role="tablist"
          aria-label={t("categoriesAria")}
          className="flex gap-2 overflow-x-auto pr-8 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
        >
          {categories.map((category) => {
            const active = category.id === activeId
            return (
              <button
                key={category.id}
                type="button"
                role="tab"
                aria-selected={active}
                onClick={() => onSelect(category.id)}
                className={cn(
                  "min-h-12 shrink-0 rounded-full border-2 px-5 text-base font-semibold whitespace-nowrap outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
                  active ? "border-foreground bg-foreground text-background" : "border-border bg-card text-foreground",
                )}
              >
                {category.name}
              </button>
            )
          })}
        </div>
        <div
          aria-hidden="true"
          className="from-background pointer-events-none absolute inset-y-0 right-0 w-8 bg-gradient-to-l to-transparent"
        />
      </div>
    </div>
  )
}

interface ProductGridProps {
  products: Product[]
  /** undefined = not known yet; true/false = has option groups. */
  hasOptions: (productId: string) => boolean | undefined
  quantityInCart: (productId: string) => number
  resolvingId: string | null
  flashId: string | null
  onTap: (product: Product) => void
}

/**
 * Product tiles: name (up to two lines, never truncated to one), price, and a
 * text label — not just an icon — when the product opens the option panel.
 * Order follows the catalog's sort_order (no client-side reordering) so tile
 * positions become muscle memory. A tile shows how many are already in the
 * cart, which is also the "it was added" feedback after a tap.
 */
export function ProductGrid({ products, hasOptions, quantityInCart, resolvingId, flashId, onTap }: ProductGridProps) {
  const t = useTranslations("posOrder")
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 2xl:grid-cols-4">
      {products.map((product) => {
        const inCart = quantityInCart(product.id)
        const options = hasOptions(product.id)
        const flashing = flashId === product.id
        const price = formatMoney(product.price_amount)
        return (
          <button
            key={product.id}
            type="button"
            onClick={() => onTap(product)}
            aria-label={t("addProductAria", { product: product.name, price })}
            aria-busy={resolvingId === product.id}
            data-testid="product-tile"
            className={cn(
              "bg-card relative flex min-h-24 flex-col justify-between gap-2 rounded-2xl border-2 p-3 text-left",
              "touch-manipulation outline-none select-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
              "transition-[transform,border-color] duration-150 active:scale-[0.97] motion-reduce:transition-none motion-reduce:active:scale-100",
              inCart > 0 ? "border-primary/60" : "border-border",
              flashing && "border-primary",
            )}
          >
            <span className="pr-9 text-base leading-snug font-semibold break-words">{product.name}</span>
            <span className="flex flex-wrap items-end justify-between gap-x-2 gap-y-1">
              <span className="text-lg font-bold tabular-nums">{price}</span>
              {options && (
                <span className="flex items-center gap-1 text-xs font-medium text-muted-foreground">
                  <SlidersHorizontal className="size-3.5" aria-hidden="true" />
                  {t("hasOptions")}
                </span>
              )}
            </span>
            {inCart > 0 && (
              <span
                aria-label={t("inCart", { count: inCart })}
                className={cn(
                  "bg-primary text-primary-foreground absolute top-2 right-2 flex h-7 min-w-7 items-center justify-center rounded-full px-2 text-sm font-bold tabular-nums",
                  flashing && "motion-safe:animate-in motion-safe:zoom-in-50 motion-safe:duration-200",
                )}
                key={flashing ? `f-${inCart}` : "s"}
              >
                {inCart}
              </span>
            )}
            {resolvingId === product.id && (
              <span className="bg-background/60 absolute inset-0 flex items-center justify-center rounded-2xl">
                <Loader2 className="size-6 animate-spin" aria-hidden="true" />
              </span>
            )}
          </button>
        )
      })}
    </div>
  )
}
