import { QrCodeIcon } from "lucide-react"
import type { Metadata } from "next"
import { getTranslations } from "next-intl/server"

export const dynamic = "force-dynamic"

export async function generateMetadata(): Promise<Metadata> {
  const t = await getTranslations("errors")
  return { title: t("title") }
}

// The single failure surface of the session flow: an unusable QR code, an
// expired session, a rate limit, or an unavailable service.
//
// The distinctions the API keeps hidden stay hidden here too — the backend
// answers unknown, revoked and relocated QR codes with one 404 so it cannot be
// used as a token oracle, and inventing three different messages in the UI
// would undo that.
const KNOWN_CODES = [
  "invalid_qr",
  "session_expired",
  "rate_limited",
  "unavailable",
  "unknown_error",
] as const

type ErrorCode = (typeof KNOWN_CODES)[number]

function asErrorCode(value: string | undefined): ErrorCode {
  return KNOWN_CODES.includes(value as ErrorCode) ? (value as ErrorCode) : "unknown_error"
}

export default async function SessionErrorPage({
  searchParams,
}: {
  searchParams: Promise<{ code?: string }>
}) {
  const { code } = await searchParams
  const t = await getTranslations("errors")
  const errorCode = asErrorCode(code)

  return (
    <main className="mx-auto flex min-h-dvh w-full max-w-screen-sm flex-col items-center justify-center gap-4 px-6 text-center">
      <QrCodeIcon className="text-muted-foreground size-12" aria-hidden="true" />
      <h1 className="text-xl font-semibold">{t("title")}</h1>
      <p className="text-base">{t(errorCode)}</p>
      <p className="text-muted-foreground text-sm">{t("rescanHint")}</p>
      <p className="text-muted-foreground text-sm">{t("staffHint")}</p>
    </main>
  )
}
