import { AxiosError, AxiosHeaders } from "axios"
import { describe, expect, it } from "vitest"

import { API_ERROR_CODES, toProblem } from "@/lib/api"

function axiosErrorWith(status: number, data: unknown, headers: Record<string, string> = {}) {
  const error = new AxiosError("request failed")
  error.response = {
    status,
    statusText: "",
    data,
    headers,
    config: { headers: new AxiosHeaders() },
  }
  return error
}

describe("toProblem", () => {
  it("reads code and detail from an RFC 7807 body", () => {
    const problem = toProblem(
      axiosErrorWith(409, {
        type: "https://errors.onlinemenu.tr/table_not_ready",
        title: "Conflict",
        status: 409,
        detail: "Masanız hazırlanıyor.",
        code: "table_not_ready",
      }),
    )
    expect(problem.code).toBe(API_ERROR_CODES.tableNotReady)
    expect(problem.detail).toBe("Masanız hazırlanıyor.")
  })

  it("survives a PLAIN TEXT error body", () => {
    // The idempotency middleware answers a missing or conflicting
    // Idempotency-Key with plain text, not problem+json (WP2 contract §3). A
    // JSON-only parser would throw on exactly the response that must not be
    // retried blindly.
    const problem = toProblem(axiosErrorWith(422, "Idempotency-Key required"))
    expect(problem.status).toBe(422)
    expect(problem.code).toBe(API_ERROR_CODES.validationFailed)
    expect(problem.detail).toBe("")
    expect(problem.retriable).toBe(false)
  })

  it("distinguishes a fail-closed rate limiter from a bad QR", () => {
    // Redis down => 503 across the whole surface, /sessions included. Telling
    // a diner their QR is invalid would send them to the counter for nothing.
    const problem = toProblem(axiosErrorWith(503, ""))
    expect(problem.code).toBe(API_ERROR_CODES.unavailable)
    expect(problem.retriable).toBe(true)
  })

  it("parses Retry-After on a 429", () => {
    const problem = toProblem(axiosErrorWith(429, "", { "retry-after": "12" }))
    expect(problem.code).toBe(API_ERROR_CODES.rateLimited)
    expect(problem.retryAfterSeconds).toBe(12)
    expect(problem.retriable).toBe(true)
  })

  it("marks a response-less failure retriable", () => {
    // The request may or may not have reached the server, so a mutation must
    // reuse its Idempotency-Key rather than start over.
    const problem = toProblem(new AxiosError("Network Error"))
    expect(problem.code).toBe(API_ERROR_CODES.network)
    expect(problem.retriable).toBe(true)
  })

  it("does not treat a 4xx as retriable", () => {
    expect(toProblem(axiosErrorWith(404, "")).retriable).toBe(false)
  })
})
