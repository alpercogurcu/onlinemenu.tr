import { act, renderHook } from "@testing-library/react"
import { beforeEach, describe, expect, it } from "vitest"

import {
  ELAPSED_LATE_MINUTES,
  ELAPSED_WARN_MINUTES,
  elapsedTone,
  formatElapsed,
} from "@/lib/kds-format"
import { useDeviceDark } from "@/lib/use-device-dark"

const NOW = Date.parse("2026-09-06T12:00:00Z")
const minutesAgo = (m: number) => new Date(NOW - m * 60_000).toISOString()

describe("kds-format", () => {
  it("formats elapsed time as m:ss and caps stale tickets", () => {
    expect(formatElapsed(minutesAgo(3), NOW)).toBe("3:00")
    expect(formatElapsed(minutesAgo(150), NOW)).toBe("99+ dk")
    expect(formatElapsed("not-a-date", NOW)).toBe("—")
  })

  it("maps elapsed minutes to the SLA tone thresholds", () => {
    expect(elapsedTone(minutesAgo(ELAPSED_WARN_MINUTES - 1), NOW)).toBe("normal")
    expect(elapsedTone(minutesAgo(ELAPSED_WARN_MINUTES), NOW)).toBe("warning")
    expect(elapsedTone(minutesAgo(ELAPSED_LATE_MINUTES), NOW)).toBe("late")
    expect(elapsedTone("not-a-date", NOW)).toBe("normal")
  })
})

describe("useDeviceDark", () => {
  // jsdom's localStorage is not reliably available under vitest (opaque
  // origin); an in-memory Storage keeps the test about the hook, not jsdom.
  let store: Record<string, string>
  beforeEach(() => {
    store = {}
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem: (k: string) => (k in store ? store[k] : null),
        setItem: (k: string, v: string) => {
          store[k] = String(v)
        },
        removeItem: (k: string) => {
          delete store[k]
        },
        clear: () => {
          store = {}
        },
        key: () => null,
        length: 0,
      } satisfies Storage,
    })
  })

  it("defaults to the app theme (off) and persists the toggle per device", () => {
    const { result } = renderHook(() => useDeviceDark("kds-dark"))
    expect(result.current[0]).toBe(false)

    act(() => result.current[1](true))
    expect(result.current[0]).toBe(true)
    expect(localStorage.getItem("kds-dark")).toBe("true")
  })

  it("reads a stored preference on mount", () => {
    localStorage.setItem("kds-dark", "true")
    const { result } = renderHook(() => useDeviceDark("kds-dark"))
    expect(result.current[0]).toBe(true)
  })
})
