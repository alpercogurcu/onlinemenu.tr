import { render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import SessionGuard from "@/components/auth/session-guard"
import { getAccessToken } from "@/lib/api"
import { beginSilentSso, readSessionHint } from "@/lib/session-restore"

const replace = vi.fn()

vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace }),
}))

vi.mock("@/lib/api", () => ({
  getAccessToken: vi.fn(),
}))

vi.mock("@/lib/session-restore", () => ({
  beginSilentSso: vi.fn(() => Promise.resolve()),
  readSessionHint: vi.fn(),
}))

const mockedToken = vi.mocked(getAccessToken)
const mockedHint = vi.mocked(readSessionHint)
const mockedSilentSso = vi.mocked(beginSilentSso)

function renderGuard() {
  render(
    <SessionGuard>
      <p>korumalı içerik</p>
    </SessionGuard>,
  )
}

function protectedContent() {
  return screen.queryByText("korumalı içerik")
}

describe("SessionGuard", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    window.history.replaceState({}, "", "/pos/checks?branch=1")
  })

  it("mounts the children when a session is already in memory", async () => {
    mockedToken.mockReturnValue("ctx-token")

    renderGuard()

    await waitFor(() => expect(protectedContent()).toBeInTheDocument())
    expect(replace).not.toHaveBeenCalled()
    expect(mockedSilentSso).not.toHaveBeenCalled()
  })

  it("sends an unauthenticated visitor with no Keycloak history to /login", async () => {
    mockedToken.mockReturnValue(null)
    mockedHint.mockReturnValue(null)

    renderGuard()

    await waitFor(() => expect(replace).toHaveBeenCalledWith("/login"))
    expect(mockedSilentSso).not.toHaveBeenCalled()
    expect(protectedContent()).not.toBeInTheDocument()
  })

  it("silently restores from the Keycloak session and keeps the requested path", async () => {
    mockedToken.mockReturnValue(null)
    mockedHint.mockReturnValue({ membershipId: "mem-1" })

    renderGuard()

    await waitFor(() => expect(mockedSilentSso).toHaveBeenCalledWith("/pos/checks?branch=1"))
    expect(replace).not.toHaveBeenCalled()
    // The children must stay unmounted while the browser navigates away,
    // otherwise their queries fire unauthenticated — the 401 storm this
    // guard exists to prevent.
    expect(protectedContent()).not.toBeInTheDocument()
  })

  it("falls back to /login when the silent restore cannot be started", async () => {
    mockedToken.mockReturnValue(null)
    mockedHint.mockReturnValue({ membershipId: "mem-1" })
    mockedSilentSso.mockRejectedValueOnce(new Error("crypto unavailable"))

    renderGuard()

    await waitFor(() => expect(replace).toHaveBeenCalledWith("/login"))
  })
})

