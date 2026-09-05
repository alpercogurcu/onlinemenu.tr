// Cosmetic permission gate on QRCodeDialog's manage actions (create/rotate/
// revoke): a cashier holds storefront.qr.read (sees the dialog and the
// active code) but not storefront.qr.manage — see lib/permissions.ts and
// authz.rego's storefront_qr_manage_actions. This is the substitute for a
// live-browser check used here: no browser tool is wired into this session,
// and the dev stack (Keycloak/API/OPA) is only partially up locally.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { QRCodeDialog } from "@/components/storefront/qr-code-dialog"
import { TooltipProvider } from "@/components/ui/tooltip"
import messages from "@/messages/tr.json"
import { useAuthStore } from "@/store/auth-store"

const { getToken, setToken } = vi.hoisted(() => {
  let token: string | null = null
  return {
    getToken: () => token,
    setToken: (t: string | null) => {
      token = t
    },
  }
})

// jsdom has no ResizeObserver; @radix-ui/react-use-size (used by the
// Tooltip content this test renders) needs one to mount. Scoped to this file
// only — not added to test/setup.ts, which is out of scope for this change.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal("ResizeObserver", ResizeObserverStub)

const get = vi.fn()

// Full replacement of lib/api: the default export backs useQRCodes/
// useCreateQRCode/etc. (hooks/use-storefront.ts), and the named exports back
// both auth-store.ts's setSession/logout AND lib/permissions.ts's token
// read — the same in-memory token both go through in the real app.
vi.mock("@/lib/api", () => ({
  default: {
    get: (...args: unknown[]) => get(...args),
    post: vi.fn(),
  },
  getAccessToken: () => getToken(),
  setAccessToken: (t: string | null) => setToken(t),
  clearAccessToken: () => setToken(null),
}))

function base64UrlEncode(obj: unknown): string {
  return Buffer.from(JSON.stringify(obj)).toString("base64url")
}

// Same 3-segment base64url shape as a real CTX token (context_token.go's
// `sign`) — only the `rids` claim matters here, signature is a placeholder.
function ctxToken(roleIds: string[]): string {
  const header = base64UrlEncode({ alg: "HS256", typ: "CTX" })
  const payload = base64UrlEncode({ rids: roleIds })
  return `${header}.${payload}.fake-signature`
}

const CASHIER_ID = "00000001-0000-0000-0000-000000000001"
const SHIFT_MANAGER_ID = "00000001-0000-0000-0000-000000000002"

function Wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <NextIntlClientProvider locale="tr" messages={messages}>
      <TooltipProvider>
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      </TooltipProvider>
    </NextIntlClientProvider>
  )
}

function loginAs(roleIds: string[]) {
  useAuthStore
    .getState()
    .setSession(ctxToken(roleIds), { id: "person-1", name: "Test", email: "test@onlinemenu.tr" }, "tenant-1")
}

function renderDialog() {
  return render(
    <QRCodeDialog open onOpenChange={() => {}} branchId="branch-1" tableId="table-1" tableLabel="T1" />,
    { wrapper: Wrapper },
  )
}

describe("QRCodeDialog manage-action gating", () => {
  beforeEach(() => {
    get.mockReset()
    get.mockResolvedValue({ data: [] })
  })

  afterEach(() => {
    useAuthStore.getState().logout()
  })

  it("disables QR üret for a cashier (storefront.qr.read only, no manage)", async () => {
    loginAs([CASHIER_ID])
    renderDialog()

    const createButton = await screen.findByRole("button", { name: /QR üret/ })
    expect(createButton).toBeDisabled()
  })

  it("enables QR üret for a shift_manager (holds storefront.qr.manage)", async () => {
    loginAs([SHIFT_MANAGER_ID])
    renderDialog()

    const createButton = await screen.findByRole("button", { name: /QR üret/ })
    expect(createButton).not.toBeDisabled()
  })

  it("keeps QR üret disabled with no session at all", async () => {
    renderDialog()

    const createButton = await screen.findByRole("button", { name: /QR üret/ })
    expect(createButton).toBeDisabled()
  })
})
