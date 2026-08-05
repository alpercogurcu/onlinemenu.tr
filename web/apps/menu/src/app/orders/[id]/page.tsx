import type { Metadata } from "next"
import { getTranslations } from "next-intl/server"
import { Suspense } from "react"

import { AppShell } from "@/components/app-shell"
import { OrderDetail } from "@/components/orders/order-detail"

export const dynamic = "force-dynamic"

export async function generateMetadata(): Promise<Metadata> {
  const t = await getTranslations("orders")
  return { title: t("detailTitle") }
}

export default async function OrderDetailPage({
  params,
}: {
  params: Promise<{ id: string }>
}) {
  const { id } = await params

  return (
    <AppShell>
      {/* OrderDetail reads useSearchParams (the ?placed=1 confirmation flag),
          which App Router requires to sit under a Suspense boundary. */}
      <Suspense fallback={null}>
        <OrderDetail orderId={id} />
      </Suspense>
    </AppShell>
  )
}
