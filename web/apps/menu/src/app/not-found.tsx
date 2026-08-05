import Link from "next/link"
import { getTranslations } from "next-intl/server"

import { Button } from "@onlinemenu/ui-kit"

export default async function NotFound() {
  const t = await getTranslations("notFound")

  return (
    <main className="mx-auto flex min-h-dvh w-full max-w-screen-sm flex-col items-center justify-center gap-4 px-6 text-center">
      <h1 className="text-xl font-semibold">{t("title")}</h1>
      <p className="text-muted-foreground text-sm">{t("description")}</p>
      <Button asChild size="touch" variant="outline">
        <Link href="/menu">{t("backToMenu")}</Link>
      </Button>
    </main>
  )
}
