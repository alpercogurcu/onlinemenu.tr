"use client"

import { useTranslations } from "next-intl"
import Image from "next/image"
import { useState } from "react"

import { Badge, Skeleton, cn } from "@onlinemenu/ui-kit"

import { CartBar } from "@/components/menu/cart-bar"
import { CategoryNav, sectionDomId } from "@/components/menu/category-nav"
import { OrderingClosedNotice } from "@/components/menu/ordering-closed-notice"
import { ProductSheet } from "@/components/menu/product-sheet"
import { ErrorState, RetryButton, useProblemMessage } from "@/components/error-state"
import { useMenu } from "@/hooks/use-storefront"
import { toProblem } from "@/lib/api"
import { imageUrl } from "@/lib/media"
import { formatKurus } from "@/lib/money"
import type { MenuCategory, MenuProduct } from "@/types/storefront"

export function MenuScreen() {
  const t = useTranslations("menu")
  const tCommon = useTranslations("common")
  const tErrors = useTranslations("errors")
  const problemMessage = useProblemMessage()
  const { data, isPending, isError, error, refetch } = useMenu()
  const [selected, setSelected] = useState<MenuProduct | null>(null)

  if (isPending) return <MenuSkeleton />

  if (isError) {
    return (
      <ErrorState
        title={tErrors("menuLoadFailed")}
        message={problemMessage(toProblem(error))}
        action={<RetryButton label={tCommon("retry")} onRetry={() => void refetch()} />}
      />
    )
  }

  const { categories, orderingEnabled } = data

  if (categories.length === 0) {
    return (
      <div className="py-16 text-center">
        <p className="text-base font-medium">{t("empty")}</p>
        <p className="text-muted-foreground mt-1 text-sm">{t("emptyHint")}</p>
      </div>
    )
  }

  // Boş ad, API'nin belgelediği sentetik "kategorisiz" kovasıdır; etiketi
  // sunucudan değil görünümden gelir. Aynı başlık hem bölümde hem çubukta
  // kullanılır ki dokunulan çip ile varılan başlık birebir aynı olsun.
  const titled = categories.map((category) => ({
    category,
    title: category.name === "" ? tCommon("otherCategory") : category.name,
  }))

  return (
    <>
      {orderingEnabled ? null : (
        <div className="pt-4">
          <OrderingClosedNotice />
        </div>
      )}
      {titled.length > 1 ? (
        <CategoryNav categories={titled.map(({ category, title }) => ({ id: category.id, title }))} />
      ) : null}
      <div className="flex flex-col gap-8 py-4">
        {titled.map(({ category, title }) => (
          <CategorySection
            key={category.id}
            category={category}
            title={title}
            onSelect={(product) => setSelected(product)}
          />
        ))}
      </div>

      <ProductSheet
        product={selected}
        orderingEnabled={orderingEnabled}
        onClose={() => setSelected(null)}
      />
      {/* The bar is the path INTO ordering; while ordering is closed it is
          withheld even for a cart with leftover lines, so the only visible
          affordances match what the diner can actually do. */}
      {orderingEnabled ? <CartBar /> : null}
    </>
  )
}

function CategorySection({
  category,
  title,
  onSelect,
}: {
  category: MenuCategory
  title: string
  onSelect: (product: MenuProduct) => void
}) {
  return (
    // scroll-mt: sticky header (56px) + kategori çubuğu (~48px) başlığı
    // örtmesin diye; çipten atlayınca başlık tam ikisinin altına oturur.
    <section
      id={sectionDomId(category.id)}
      aria-labelledby={`category-${category.id}`}
      className="scroll-mt-28"
    >
      <h2 id={`category-${category.id}`} className="mb-3 text-lg font-semibold">
        {title}
      </h2>
      <ul className="flex flex-col gap-2">
        {category.products.map((product) => (
          <li key={product.id}>
            <ProductRow product={product} onSelect={onSelect} />
          </li>
        ))}
      </ul>
    </section>
  )
}

function ProductRow({
  product,
  onSelect,
}: {
  product: MenuProduct
  onSelect: (product: MenuProduct) => void
}) {
  const t = useTranslations("menu")
  const src = imageUrl(product.image_key)

  return (
    <button
      type="button"
      // is_available=false products stay visible but unorderable: hiding them
      // makes a diner ask staff why the thing they came for is missing.
      disabled={!product.is_available}
      aria-disabled={!product.is_available}
      onClick={() => onSelect(product)}
      className={cn(
        "bg-card flex w-full items-center gap-3 rounded-xl border p-3 text-left transition-colors",
        product.is_available ? "active:bg-accent" : "opacity-50",
      )}
    >
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-base font-medium">{product.name}</span>
          {product.is_available ? null : (
            <Badge variant="secondary" className="shrink-0">
              {t("unavailable")}
            </Badge>
          )}
        </div>
        {product.description === "" ? null : (
          <p className="text-muted-foreground mt-0.5 line-clamp-2 text-sm">
            {product.description}
          </p>
        )}
        <span className="mt-1 block text-base font-semibold tabular-nums">
          {formatKurus(product.price_amount)}
        </span>
      </div>

      {src === null ? null : (
        <Image
          src={src}
          alt=""
          width={72}
          height={72}
          unoptimized
          className="size-18 shrink-0 rounded-lg object-cover"
        />
      )}
    </button>
  )
}

function MenuSkeleton() {
  return (
    <div className="flex flex-col gap-6 py-4" aria-busy="true">
      {[0, 1, 2].map((section) => (
        <div key={section} className="flex flex-col gap-2">
          <Skeleton className="h-6 w-40" />
          <Skeleton className="h-20 w-full rounded-xl" />
          <Skeleton className="h-20 w-full rounded-xl" />
        </div>
      ))}
    </div>
  )
}
