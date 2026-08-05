import { NextResponse, type NextRequest } from "next/server"

import { TABLE_COOKIE_NAME, encodeTableSession } from "@/lib/table-session"
import type { SessionResponse } from "@/types/storefront"

// This is a Route Handler, not a page, for a reason that is not stylistic: a
// Server Component cannot set cookies in the App Router, and establishing the
// session IS setting a cookie. Rendering a page that then POSTs from the
// browser would work too, but would put the QR token into client-side
// JavaScript for no gain.
export const dynamic = "force-dynamic"

// Server-to-server origin. Never NEXT_PUBLIC_: the browser reaches the API
// through the same-origin rewrite in next.config.ts.
const API_ORIGIN = process.env.PUBLIC_API_ORIGIN ?? "http://localhost:8081"

const SESSIONS_ENDPOINT = "/api/public/v1/sessions"

/** Error codes understood by /q/error. Kept in sync with messages/tr.json. */
const ERROR_INVALID_QR = "invalid_qr"
const ERROR_RATE_LIMITED = "rate_limited"
const ERROR_UNAVAILABLE = "unavailable"
const ERROR_UNKNOWN = "unknown_error"

export async function GET(
  request: NextRequest,
  context: { params: Promise<{ token: string }> },
) {
  const { token } = await context.params

  if (token === "") {
    return redirectToError(ERROR_INVALID_QR)
  }

  let apiResponse: Response
  try {
    apiResponse = await fetch(`${API_ORIGIN}${SESSIONS_ENDPOINT}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      // The token travels in the BODY. Never in a path or a query string:
      // otelhttp records the raw request path as a span attribute before chi
      // routes it, and a URL-borne token would land in traces, access logs and
      // the browser's Referer header (ADR-ARCH-006 §3, plan note D1).
      body: JSON.stringify({ token }),
      cache: "no-store",
    })
  } catch {
    // The API is unreachable from this server. Deliberately not logged with
    // the token in scope.
    return redirectToError(ERROR_UNAVAILABLE)
  }

  if (!apiResponse.ok) {
    return redirectToError(errorCodeForStatus(apiResponse.status))
  }

  let session: SessionResponse
  try {
    session = (await apiResponse.json()) as SessionResponse
  } catch {
    return redirectToError(ERROR_UNKNOWN)
  }

  const response = seeOther("/menu")

  // Forward the API's Set-Cookie verbatim. Its attributes (HttpOnly, Secure,
  // SameSite, Path=/api/public/v1, expiry) are the backend's decision and are
  // NOT rewritten here — widening the Path would attach a guest session to
  // every staff request the same browser makes, which is exactly what the
  // narrow scope exists to prevent.
  //
  // This only works because the browser talks to the API through this app's
  // origin (the rewrite in next.config.ts). A host-only cookie handed back
  // here is bound to the menu host, which is also the host every later API
  // call uses.
  //
  // Raw header appends, NOT response.cookies.set(): NextResponse's cookie
  // helper re-serializes the whole Set-Cookie header from its own parsed
  // model, which DROPPED this forwarded cookie outright (verified against a
  // stub API) and percent-encoded the companion cookie a second time. The
  // guest session must survive this hop byte for byte.
  for (const cookie of apiResponse.headers.getSetCookie()) {
    response.headers.append("set-cookie", cookie)
  }

  // POST /sessions is the only place the table label is ever returned, and
  // there is no GET /sessions to ask again. `om_guest` is HttpOnly, so the
  // client cannot read it back out — the label is mirrored into a companion
  // cookie that carries nothing secret (no QR token, no session token) and
  // grants nothing.
  response.headers.append(
    "set-cookie",
    serializeTableCookie(session, request.nextUrl.protocol === "https:"),
  )

  response.headers.set("Cache-Control", "no-store")

  return response
}

function serializeTableCookie(session: SessionResponse, secure: boolean): string {
  const value = encodeTableSession({
    tableLabel: session.table_label,
    expiresAt: session.expires_at,
  })
  const attributes = [
    `${TABLE_COOKIE_NAME}=${value}`,
    "Path=/",
    `Expires=${expiryOrDefault(session.expires_at).toUTCString()}`,
    "SameSite=Lax",
  ]
  // Deliberately NOT HttpOnly: the header reads this from JS. It is not a
  // credential — the backend does not know it exists.
  if (secure) attributes.push("Secure")
  return attributes.join("; ")
}

function errorCodeForStatus(status: number): string {
  switch (status) {
    case 400:
    case 404:
      // The backend answers unknown, revoked and moved QR codes with the same
      // 404 on purpose, so it cannot be used as a token oracle. The UI keeps
      // that distinction collapsed.
      return ERROR_INVALID_QR
    case 429:
      return ERROR_RATE_LIMITED
    case 503:
      // The public rate limiter fails closed when Redis is down and takes
      // /sessions with it — a service outage, not a bad QR code.
      return ERROR_UNAVAILABLE
    default:
      return status >= 500 ? ERROR_UNAVAILABLE : ERROR_UNKNOWN
  }
}

function redirectToError(code: string) {
  const response = seeOther(`/q/error?code=${encodeURIComponent(code)}`)
  response.headers.set("Cache-Control", "no-store")
  return response
}

/**
 * 303 with a RELATIVE Location.
 *
 * NextResponse.redirect() demands an absolute URL, and building one from
 * request.nextUrl reproduces the server's own bind address — behind a reverse
 * proxy (the production topology: see deploy/docker-compose.prod.yml) that
 * sent the browser to `http://0.0.0.0:3001/menu`, which was observed against
 * the standalone build. A relative Location is resolved by the browser against
 * the URL it actually requested, so it is correct behind any proxy.
 *
 * 303 rather than 307: the browser must follow this with a GET.
 */
function seeOther(location: string): NextResponse {
  return new NextResponse(null, { status: 303, headers: { Location: location } })
}

function expiryOrDefault(expiresAt: string): Date {
  const parsed = new Date(expiresAt)
  if (Number.isNaN(parsed.getTime())) {
    // Matches the backend's 4-hour guest session; only reached if the API ever
    // sends an unparseable timestamp.
    return new Date(Date.now() + 4 * 60 * 60 * 1000)
  }
  return parsed
}
