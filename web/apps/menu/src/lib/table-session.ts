// `om_guest` is HttpOnly, so the browser can neither read the session nor
// recover what table it belongs to — and there is no GET /sessions to ask.
// POST /sessions returns the table label exactly once, server-side, inside the
// /q/[token] route handler.
//
// This companion cookie carries that label onward. It holds nothing secret:
// no QR token, no session token, no ids that grant anything. It is readable by
// JS ON PURPOSE (the header renders from it) and is never sent as a
// credential — the backend does not know it exists.

export const TABLE_COOKIE_NAME = "om_table"

export interface TableSessionInfo {
  tableLabel: string
  /** ISO-8601 expiry of the guest session, mirrored from POST /sessions. */
  expiresAt: string
}

export function encodeTableSession(info: TableSessionInfo): string {
  return encodeURIComponent(JSON.stringify(info))
}

export function decodeTableSession(raw: string): TableSessionInfo | null {
  try {
    const parsed: unknown = JSON.parse(decodeURIComponent(raw))
    if (parsed === null || typeof parsed !== "object") return null
    const { tableLabel, expiresAt } = parsed as Record<string, unknown>
    if (typeof tableLabel !== "string" || typeof expiresAt !== "string") return null
    return { tableLabel, expiresAt }
  } catch {
    // A truncated or hand-edited cookie is not an error worth surfacing: the
    // header simply renders without a table name.
    return null
  }
}

/** Client-side read. Returns null on the server, where document is undefined. */
export function readTableSession(): TableSessionInfo | null {
  if (typeof document === "undefined") return null
  const match = document.cookie
    .split("; ")
    .find((part) => part.startsWith(`${TABLE_COOKIE_NAME}=`))
  if (match === undefined) return null
  return decodeTableSession(match.slice(TABLE_COOKIE_NAME.length + 1))
}
