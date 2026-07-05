import { NextResponse } from "next/server"

import { exchangeAuthorizationCode } from "@/lib/keycloak-server"

interface TokenRequestBody {
  code?: string
  code_verifier?: string
  redirect_uri?: string
}

// Same-origin route handler: browser -> here (no CORS) -> Keycloak token
// endpoint (server-to-server, CORS does not apply). See keycloak-server.ts
// for why this indirection exists. PKCE only — never accepts/sends a
// client_secret (admin-panel is a public client).
export async function POST(request: Request) {
  let body: TokenRequestBody
  try {
    body = (await request.json()) as TokenRequestBody
  } catch {
    return NextResponse.json({ error: "invalid json body" }, { status: 400 })
  }

  if (!body.code || !body.code_verifier || !body.redirect_uri) {
    return NextResponse.json(
      { error: "code, code_verifier and redirect_uri are required" },
      { status: 400 },
    )
  }

  try {
    const tokens = await exchangeAuthorizationCode({
      code: body.code,
      codeVerifier: body.code_verifier,
      redirectUri: body.redirect_uri,
    })
    return NextResponse.json(tokens)
  } catch (err) {
    return NextResponse.json(
      {
        error: "token exchange failed",
        detail: err instanceof Error ? err.message : String(err),
      },
      { status: 401 },
    )
  }
}
