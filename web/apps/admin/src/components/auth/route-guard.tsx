"use client"

import { ShieldOff } from "lucide-react"
import { useTranslations } from "next-intl"

import { type ReactNode, useEffect } from "react"

import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"

import { Button } from "@/components/ui/button"
import { canAccessRoute, currentHomeRoute } from "@/lib/route-permissions"
import { useAuthStore } from "@/store/auth-store"

export function AccessDenied({ home }: { home: string | null }) {
  const t = useTranslations("navigation.accessDenied")
  return (
    <div
      role="alert"
      data-testid="access-denied"
      className="mx-auto mt-16 flex max-w-md flex-col items-center gap-4 text-center"
    >
      <ShieldOff className="size-10 text-muted-foreground" aria-hidden />
      <h1 className="text-xl font-semibold">{t("title")}</h1>
      <p className="text-sm text-muted-foreground">{t("body")}</p>
      {home && (
        <Button asChild variant="outline">
          <Link href={home}>{t("home")}</Link>
        </Button>
      )}
    </div>
  )
}

/**
 * Screen-level gate for app/(main). Renders the denied state INSTEAD of the
 * page, so a forbidden page's hooks never mount and no request reaches the
 * API. The dashboard ("/") is the one exception: without access it forwards
 * to the role's landing screen rather than showing a dead end, because "/" is
 * where every sign-in lands.
 *
 * Cosmetic only (see lib/permissions.ts) — the backend enforces regardless.
 */
export default function RouteGuard({ children }: { children: ReactNode }) {
  const pathname = usePathname()
  const router = useRouter()
  // Re-render when the session (and thus the CTX token read below) changes.
  useAuthStore((s) => s.user)

  const allowed = canAccessRoute(pathname)
  const home = currentHomeRoute()
  const redirectHome = !allowed && pathname === "/" && home !== null && home !== "/"

  useEffect(() => {
    if (redirectHome && home) router.replace(home)
  }, [redirectHome, home, router])

  if (allowed) return <>{children}</>
  if (redirectHome) return null
  return <AccessDenied home={home} />
}
