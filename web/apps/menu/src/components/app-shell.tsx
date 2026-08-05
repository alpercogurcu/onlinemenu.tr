"use client"

import { ClipboardListIcon, UtensilsCrossedIcon } from "lucide-react"
import { useTranslations } from "next-intl"
import Link from "next/link"
import { usePathname } from "next/navigation"
import { useEffect, type ReactNode } from "react"

import { cn } from "@onlinemenu/ui-kit"

import { useTableSession } from "@/hooks/use-table-session"
import { hydrateCartForSession } from "@/lib/cart-store"

/**
 * Header + cart hydration for every in-session screen.
 *
 * Hydration lives here rather than in the store module: the persisted cart
 * outlives a guest session, so restoring it and deciding whether it still
 * belongs to this diner must happen together, on the client, after the session
 * cookie has actually been read.
 */
export function AppShell({ children }: { children: ReactNode }) {
  const t = useTranslations("header")
  const pathname = usePathname()
  const table = useTableSession()
  const sessionKey = table?.expiresAt ?? null

  useEffect(() => {
    // Null means the cookie has not been read yet (the server snapshot), not
    // that there is no session — hydration simply waits for the real value.
    // expiresAt is unique per issued session (it is the guest token's exp),
    // which makes it a session identity that exposes nothing the HttpOnly
    // cookie holds.
    if (sessionKey === null) return
    void hydrateCartForSession(sessionKey)
  }, [sessionKey])

  return (
    <div className="flex min-h-dvh flex-col">
      <header className="bg-background/95 sticky top-0 z-30 border-b backdrop-blur">
        <div className="mx-auto flex h-14 w-full max-w-screen-sm items-center gap-2 px-4">
          <span className="truncate text-base font-semibold">
            {table ? t("table", { label: table.tableLabel }) : t("menu")}
          </span>
          <nav className="ml-auto flex items-center gap-1">
            <NavLink
              href="/menu"
              active={pathname === "/menu"}
              label={t("menu")}
              icon={<UtensilsCrossedIcon className="size-5" aria-hidden="true" />}
            />
            <NavLink
              href="/orders"
              active={pathname.startsWith("/orders")}
              label={t("orders")}
              icon={<ClipboardListIcon className="size-5" aria-hidden="true" />}
            />
          </nav>
        </div>
      </header>

      <main className="mx-auto w-full max-w-screen-sm flex-1 px-4 pb-28">{children}</main>
    </div>
  )
}

function NavLink({
  href,
  active,
  label,
  icon,
}: {
  href: string
  active: boolean
  label: string
  icon: ReactNode
}) {
  return (
    <Link
      href={href}
      aria-label={label}
      aria-current={active ? "page" : undefined}
      className={cn(
        "flex size-11 items-center justify-center rounded-lg transition-colors",
        active ? "bg-accent text-accent-foreground" : "text-muted-foreground",
      )}
    >
      {icon}
    </Link>
  )
}
