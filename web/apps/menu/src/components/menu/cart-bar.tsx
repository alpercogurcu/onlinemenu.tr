"use client"

import { ShoppingBagIcon } from "lucide-react"
import { useTranslations } from "next-intl"
import Link from "next/link"

import { Button } from "@onlinemenu/ui-kit"

import { cartItemCount, cartTotal, useCartStore } from "@/lib/cart-store"
import { formatKurus } from "@/lib/money"

/**
 * The single always-reachable path from browsing to ordering.
 *
 * Fixed to the bottom edge because that is where a thumb is when a phone is
 * held one-handed at a table — the primary action of this app must never
 * require scrolling to find.
 */
export function CartBar() {
  const t = useTranslations("menu")
  const lines = useCartStore((s) => s.lines)

  if (lines.length === 0) return null

  const count = cartItemCount(lines)
  const total = cartTotal(lines)

  return (
    <div className="fixed inset-x-0 bottom-0 z-40 px-4 pb-[calc(env(safe-area-inset-bottom)+1rem)]">
      <div className="mx-auto w-full max-w-screen-sm">
        <Button asChild size="touch-lg" className="w-full shadow-lg">
          <Link href="/cart">
            <ShoppingBagIcon aria-hidden="true" />
            <span className="flex-1 text-left">{t("itemCount", { count })}</span>
            <span className="tabular-nums">{formatKurus(total)}</span>
          </Link>
        </Button>
      </div>
    </div>
  )
}
