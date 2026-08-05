import type { Metadata } from "next"
import { getTranslations } from "next-intl/server"

import { AppShell } from "@/components/app-shell"
import { OrdersScreen } from "@/components/orders/orders-screen"

export const dynamic = "force-dynamic"

export async function generateMetadata(): Promise<Metadata> {
  const t = await getTranslations("orders")
  return { title: t("title") }
}

export default function OrdersPage() {
  return (
    <AppShell>
      <OrdersScreen />
    </AppShell>
  )
}
