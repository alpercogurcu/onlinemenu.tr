"use client"

import { useCallback, useEffect, useState } from "react"

// A per-device dark-mode preference for a single screen (the kitchen display
// runs on a wall tablet that may want dark regardless of the operator's own
// theme). It is deliberately NOT next-themes: it must not touch `html.dark`
// or the rest of the app — the caller scopes it to its own root element.
export function useDeviceDark(storageKey: string): [boolean, (next: boolean) => void] {
  const [enabled, setEnabled] = useState(false)

  useEffect(() => {
    try {
      setEnabled(localStorage.getItem(storageKey) === "true")
    } catch {
      // Storage unavailable (private mode, sandbox): stay on the app theme.
    }
  }, [storageKey])

  const update = useCallback(
    (next: boolean) => {
      setEnabled(next)
      try {
        localStorage.setItem(storageKey, String(next))
      } catch {
        // Same as above — the toggle still works for this page load.
      }
    },
    [storageKey],
  )

  return [enabled, update]
}
