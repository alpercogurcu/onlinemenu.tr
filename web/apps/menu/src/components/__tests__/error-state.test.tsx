import { renderHook } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { describe, expect, it } from "vitest"

import { useProblemMessage } from "@/components/error-state"
import type { ApiProblem } from "@/lib/api"
import messages from "@/messages/tr.json"

function wrapper({ children }: { children: React.ReactNode }) {
  return (
    <NextIntlClientProvider locale="tr" messages={messages}>
      {children}
    </NextIntlClientProvider>
  )
}

function problemWith(code: string): ApiProblem {
  return { status: 409, code, detail: "", retryAfterSeconds: null, retriable: false }
}

describe("useProblemMessage", () => {
  it("sunucunun ordering_disabled 409'unu şeritteki insani mesaja mapler", () => {
    const { result } = renderHook(() => useProblemMessage(), { wrapper })
    expect(result.current(problemWith("ordering_disabled"))).toBe(
      messages.errors.ordering_disabled,
    )
  })
})
