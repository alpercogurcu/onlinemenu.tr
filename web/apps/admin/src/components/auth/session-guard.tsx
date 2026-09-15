"use client"

import { useRouter } from "next/navigation"

import { type ReactNode, useEffect, useRef, useState } from "react"

import { Skeleton } from "@/components/ui/skeleton"
import { getAccessToken } from "@/lib/api"
import { beginSilentSso, readSessionHint } from "@/lib/session-restore"

function PendingShell() {
  return (
    <div className="flex min-h-screen w-full" role="status" aria-busy="true">
      <span className="sr-only">Oturum doğrulanıyor</span>
      <div className="hidden w-[16rem] flex-col gap-2 border-r p-2 md:flex">
        {Array.from({ length: 8 }).map((_, i) => (
          <Skeleton key={i} className="h-8 w-full" />
        ))}
      </div>
      <div className="flex-1">
        <div className="h-16 border-b" />
      </div>
    </div>
  )
}

/**
 * Gate for every authenticated route group.
 *
 * Tokens live in memory only (lib/api.ts, lib/keycloak-token-store.ts), so a
 * reload, a new tab or a pasted deep link starts with no session. Without a
 * gate the whole shell mounted anyway and every query fired unauthenticated —
 * a wall of 401s, an empty UI and no way for the user to tell they were
 * logged out.
 *
 * Nothing below this component mounts until a session exists: either the one
 * already in memory, or one restored silently from the Keycloak SSO cookie.
 * When neither is possible the user goes to /login instead of a dead shell.
 */
export default function SessionGuard({ children }: { children: ReactNode }) {
  const router = useRouter()
  const [authenticated, setAuthenticated] = useState(false)

  // Restoring is a one-shot, side-effecting operation (it burns PKCE material
  // and navigates); StrictMode's double effect invocation must not run it twice.
  const startedRef = useRef(false)

  useEffect(() => {
    if (startedRef.current) return
    startedRef.current = true

    if (getAccessToken()) {
      setAuthenticated(true)
      return
    }

    // No hint means no Keycloak login ever completed in this browser — a
    // dev-login session, or a first visit. Bouncing through Keycloak would be
    // pointless here, and in dev it would hit a Keycloak that may not be running.
    if (!readSessionHint()) {
      router.replace("/login")
      return
    }

    void beginSilentSso(window.location.pathname + window.location.search).catch(() => {
      router.replace("/login")
    })
  }, [router])

  if (!authenticated) {
    return <PendingShell />
  }

  return <>{children}</>
}
