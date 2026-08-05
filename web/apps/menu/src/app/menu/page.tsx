import type { Metadata } from "next"
import { getTranslations } from "next-intl/server"

import { AppShell } from "@/components/app-shell"
import { MenuScreen } from "@/components/menu/menu-screen"

// Rendered on the CLIENT, not the server, and deliberately not cached.
//
// The guest cookie is scoped `Path=/api/public/v1` by the backend, so the
// browser never attaches it to a page navigation — a Server Component here
// literally cannot read the session, and `export const revalidate` would build
// one shared cache entry across every tenant on the most exposed surface in
// the product. Freshness is handled by TanStack Query's staleTime, which
// mirrors the API's `Cache-Control: private, max-age=30`.
export const dynamic = "force-dynamic"

export async function generateMetadata(): Promise<Metadata> {
  const t = await getTranslations("menu")
  return { title: t("title") }
}

export default function MenuPage() {
  return (
    <AppShell>
      <MenuScreen />
    </AppShell>
  )
}
