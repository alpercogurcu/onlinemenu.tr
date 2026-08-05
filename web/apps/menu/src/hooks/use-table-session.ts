"use client"

import { useMemo, useSyncExternalStore } from "react"

import {
  TABLE_COOKIE_NAME,
  decodeTableSession,
  type TableSessionInfo,
} from "@/lib/table-session"

// Cookies have no change event, so there is nothing to subscribe to. The value
// is written once, server-side, by the /q/[token] route handler and does not
// change for the life of the session.
function subscribe(): () => void {
  return () => {}
}

function getSnapshot(): string {
  if (typeof document === "undefined") return ""
  const match = document.cookie
    .split("; ")
    .find((part) => part.startsWith(`${TABLE_COOKIE_NAME}=`))
  return match ?? ""
}

// useSyncExternalStore instead of useState+useEffect: it gives the server an
// explicit empty snapshot, so the header renders without a table label during
// hydration and fills in afterwards, with no mismatch warning and no state
// write from an effect.
function getServerSnapshot(): string {
  return ""
}

export function useTableSession(): TableSessionInfo | null {
  const raw = useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
  return useMemo(() => {
    if (raw === "") return null
    return decodeTableSession(raw.slice(TABLE_COOKIE_NAME.length + 1))
  }, [raw])
}
