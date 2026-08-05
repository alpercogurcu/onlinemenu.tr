import { QrCodeIcon } from "lucide-react"
import { getTranslations } from "next-intl/server"

// There is no way into this app other than scanning a table's QR code: the
// session is established by /q/{token} and nothing else issues one. A diner
// who reaches the bare domain is told exactly that.
export default async function LandingPage() {
  const t = await getTranslations("landing")

  return (
    <main className="mx-auto flex min-h-dvh w-full max-w-screen-sm flex-col items-center justify-center gap-4 px-6 text-center">
      <QrCodeIcon className="text-muted-foreground size-12" aria-hidden="true" />
      <h1 className="text-xl font-semibold">{t("title")}</h1>
      <p className="text-base">{t("description")}</p>
      <p className="text-muted-foreground text-sm">{t("hint")}</p>
    </main>
  )
}
