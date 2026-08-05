import type { Metadata } from "next"
import { getTranslations } from "next-intl/server"

import { AppShell } from "@/components/app-shell"
import { CartScreen } from "@/components/cart/cart-screen"

export const dynamic = "force-dynamic"

export async function generateMetadata(): Promise<Metadata> {
  const t = await getTranslations("cart")
  return { title: t("title") }
}

export default function CartPage() {
  return (
    <AppShell>
      <CartScreen />
    </AppShell>
  )
}
