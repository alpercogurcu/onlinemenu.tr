import axios, { type AxiosError } from "axios"

// The guest identity lives ONLY in the HttpOnly `om_guest` cookie. There is no
// Authorization header on this surface — the backend never reads one here
// (storefront/http/guest_middleware.go), and adding one would be the mirror of
// the mistake that guard exists to prevent.
//
// baseURL defaults to "" (same origin) because next.config.ts proxies
// /api/public/v1/* to the API. That keeps the cookie's `Path=/api/public/v1`
// scope intact and avoids CORS, which the backend only emits in dev.
const api = axios.create({
  baseURL: process.env.NEXT_PUBLIC_PUBLIC_API_URL ?? "",
  headers: { "Content-Type": "application/json" },
  withCredentials: true,
})

export const PUBLIC_API_PREFIX = "/api/public/v1"

// Stable machine-readable codes from storefront/http/errors.go. The client
// branches on these; `detail` is diner-facing Turkish text that may change.
export const API_ERROR_CODES = {
  notFound: "not_found",
  unauthorized: "unauthorized",
  invalidRequest: "invalid_request",
  validationFailed: "validation_failed",
  tableNotReady: "table_not_ready",
  tableOccupied: "table_occupied",
  tableBranchMismatch: "table_branch_mismatch",
  internal: "internal_error",
  // Not produced by the backend — synthesised below for transport-level
  // failures so every caller can branch on a single field.
  rateLimited: "rate_limited",
  unavailable: "unavailable",
  network: "network_error",
  unknown: "unknown_error",
} as const

export interface ApiProblem {
  status: number
  code: string
  /** Diner-facing text from the server, when it sent one. */
  detail: string
  /** Seconds from a 429's Retry-After header, when present. */
  retryAfterSeconds: number | null
  /** True when retrying the exact same request could still succeed. */
  retriable: boolean
}

function parseRetryAfter(value: unknown): number | null {
  if (typeof value !== "string") return null
  const seconds = Number.parseInt(value, 10)
  return Number.isFinite(seconds) && seconds >= 0 ? seconds : null
}

/**
 * Normalises anything axios can throw into one shape.
 *
 * Not every error body is problem+json: the idempotency middleware answers a
 * missing/conflicting `Idempotency-Key` with PLAIN TEXT (see the WP2 contract),
 * so a JSON-only parser would crash on exactly the request that must not be
 * retried blindly.
 */
export function toProblem(error: unknown): ApiProblem {
  if (!axios.isAxiosError(error)) {
    return {
      status: 0,
      code: API_ERROR_CODES.unknown,
      detail: "",
      retryAfterSeconds: null,
      retriable: false,
    }
  }

  const axiosError = error as AxiosError<unknown>
  const response = axiosError.response

  if (!response) {
    // No response at all: DNS, offline, aborted. The request may or may not
    // have reached the server, so a mutation MUST reuse its Idempotency-Key.
    return {
      status: 0,
      code: API_ERROR_CODES.network,
      detail: "",
      retryAfterSeconds: null,
      retriable: true,
    }
  }

  const body = response.data
  const problem =
    body !== null && typeof body === "object"
      ? (body as { code?: unknown; detail?: unknown })
      : null

  const code =
    typeof problem?.code === "string" && problem.code !== ""
      ? problem.code
      : defaultCodeForStatus(response.status)

  return {
    status: response.status,
    code,
    detail: typeof problem?.detail === "string" ? problem.detail : "",
    retryAfterSeconds: parseRetryAfter(response.headers?.["retry-after"]),
    retriable: response.status === 429 || response.status >= 500,
  }
}

/** Where a diner is sent when their session is gone. Exported for tests. */
export const SESSION_EXPIRED_PATH = "/q/error?code=session_expired"

// A 401 on this surface has exactly one meaning: the guest cookie is missing,
// malformed or expired (storefront/http/guest_middleware.go refuses to say
// which). The cookie is HttpOnly, so no client-side check can pre-empt it —
// the only way to learn about it is to be told, and the only recovery is to
// re-scan the QR code.
api.interceptors.response.use(
  (response) => response,
  (error: unknown) => {
    if (
      typeof window !== "undefined" &&
      axios.isAxiosError(error) &&
      error.response?.status === 401 &&
      !window.location.pathname.startsWith("/q/")
    ) {
      window.location.href = SESSION_EXPIRED_PATH
    }
    return Promise.reject(error)
  },
)

function defaultCodeForStatus(status: number): string {
  switch (status) {
    case 401:
      return API_ERROR_CODES.unauthorized
    case 404:
      return API_ERROR_CODES.notFound
    case 422:
      return API_ERROR_CODES.validationFailed
    case 429:
      return API_ERROR_CODES.rateLimited
    case 503:
      // The rate limiter fails CLOSED when Redis is unreachable and takes the
      // whole surface down with it — a distinct condition from "bad QR".
      return API_ERROR_CODES.unavailable
    default:
      return status >= 500 ? API_ERROR_CODES.internal : API_ERROR_CODES.unknown
  }
}

export default api
