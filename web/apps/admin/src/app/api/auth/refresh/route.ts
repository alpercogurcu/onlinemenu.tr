import { NextResponse } from "next/server"

import { refreshAccessToken } from "@/lib/keycloak-server"

interface RefreshRequestBody {
  refresh_token?: string
}

// Same-origin route handler used by the api.ts CTX-401 interceptor to renew
// the Keycloak session before re-deriving a fresh CTX token. See
// keycloak-server.ts for why the exchange happens server-side.
export async function POST(request: Request) {
  let body: RefreshRequestBody
  try {
    body = (await request.json()) as RefreshRequestBody
  } catch {
    return NextResponse.json({ error: "invalid json body" }, { status: 400 })
  }

  if (!body.refresh_token) {
    return NextResponse.json({ error: "refresh_token is required" }, { status: 400 })
  }

  try {
    const tokens = await refreshAccessToken(body.refresh_token)
    return NextResponse.json(tokens)
  } catch (err) {
    return NextResponse.json(
      {
        error: "refresh failed",
        detail: err instanceof Error ? err.message : String(err),
      },
      { status: 401 },
    )
  }
}
