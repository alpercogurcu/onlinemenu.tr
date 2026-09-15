import { beforeEach, describe, expect, it } from "vitest"

import {
  clearSessionHint,
  clearSilentAttempt,
  consumeReturnPath,
  consumeSilentAttempt,
  markSilentAttempt,
  readSessionHint,
  sanitizeReturnPath,
  saveReturnPath,
  saveSessionHint,
} from "@/lib/session-restore"

// jsdom's localStorage is not reliably available under vitest (opaque
// origin) — same in-memory Storage stand-in the KDS theme tests use, so the
// assertions stay about session-restore rather than about jsdom.
function installMemoryLocalStorage(): void {
  let store: Record<string, string> = {}
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
}

beforeEach(() => {
  installMemoryLocalStorage()
  sessionStorage.clear()
})

describe("session hint", () => {
  it("round-trips the membership id", () => {
    saveSessionHint("mem-1")
    expect(readSessionHint()).toEqual({ membershipId: "mem-1" })
  })

  it("survives a new tab (localStorage, not sessionStorage)", () => {
    saveSessionHint("mem-1")
    sessionStorage.clear()
    expect(readSessionHint()).toEqual({ membershipId: "mem-1" })
  })

  it("is null after clearing", () => {
    saveSessionHint("mem-1")
    clearSessionHint()
    expect(readSessionHint()).toBeNull()
  })

  it("is null when nothing was saved", () => {
    expect(readSessionHint()).toBeNull()
  })

  it.each(["not json", "{}", '{"membershipId":""}', '{"membershipId":42}'])(
    "rejects the malformed payload %j instead of throwing",
    (raw) => {
      window.localStorage.setItem("om_session_hint", raw)
      expect(readSessionHint()).toBeNull()
    },
  )
})

describe("sanitizeReturnPath", () => {
  it.each(["/pos/checks", "/", "/catalog/products?page=2", "/settings/users#tab"])(
    "accepts the in-app path %s",
    (path) => {
      expect(sanitizeReturnPath(path)).toBe(path)
    },
  )

  it.each([
    "//evil.com",
    "/\\evil.com",
    "https://evil.com/pos",
    "pos/checks",
    "",
    null,
    undefined,
  ])("rejects %j", (path) => {
    expect(sanitizeReturnPath(path)).toBeNull()
  })

  it.each(["/login", "/login/", "/login?next=/pos", "/auth", "/auth/callback", "/auth/callback?code=x"])(
    "rejects the auth-flow path %s so restoring cannot loop",
    (path) => {
      expect(sanitizeReturnPath(path)).toBeNull()
    },
  )
})

describe("return path", () => {
  it("is returned exactly once", () => {
    saveReturnPath("/pos/checks")
    expect(consumeReturnPath()).toBe("/pos/checks")
    expect(consumeReturnPath()).toBeNull()
  })

  it("is not stored at all when it fails validation", () => {
    saveReturnPath("/pos/checks")
    saveReturnPath("//evil.com")
    expect(consumeReturnPath()).toBeNull()
  })

  it("clears a tampered value on read", () => {
    sessionStorage.setItem("om_return_path", "//evil.com")
    expect(consumeReturnPath()).toBeNull()
    expect(sessionStorage.getItem("om_return_path")).toBeNull()
  })
})

describe("silent attempt flag", () => {
  it("reads true exactly once", () => {
    markSilentAttempt()
    expect(consumeSilentAttempt()).toBe(true)
    expect(consumeSilentAttempt()).toBe(false)
  })

  it("is false when no silent restore was started", () => {
    expect(consumeSilentAttempt()).toBe(false)
  })

  it("can be dropped without being consumed", () => {
    markSilentAttempt()
    clearSilentAttempt()
    expect(consumeSilentAttempt()).toBe(false)
  })
})
