// Non-secret continuity state for rebuilding a session after a full page
// load (F5, new tab, pasted deep link).
//
// Neither the CTX token nor the Keycloak tokens are ever persisted (see
// lib/api.ts and lib/keycloak-token-store.ts) — a hard navigation therefore
// starts with no session at all, even while the Keycloak SSO cookie is still
// valid. Rather than weakening that by writing tokens to storage, we persist
// only the two non-credential facts needed to silently re-run the PKCE flow
// with `prompt=none`: that a Keycloak login happened at all, and which
// membership was active. The membership id is not a secret — POST
// /v1/identity/auth/context re-authorizes it against the Keycloak identity on
// every call, so a forged value buys nothing.

import {
  buildAuthorizeUrl,
  callbackRedirectUri,
  savePkceParams,
} from "@/lib/keycloak"
import {
  generateCodeChallenge,
  generateCodeVerifier,
  generateNonce,
  generateState,
} from "@/lib/pkce"

const SESSION_HINT_KEY = "om_session_hint"
const RETURN_PATH_KEY = "om_return_path"
const SILENT_ATTEMPT_KEY = "om_silent_attempt"

export interface SessionHint {
  membershipId: string
}

// Storage access throws in Safari private mode and when a policy disables it;
// a missing hint degrades to "send the user to /login", never to a crash.
function safeGet(store: Storage | undefined, key: string): string | null {
  try {
    return store?.getItem(key) ?? null
  } catch {
    return null
  }
}

function safeSet(store: Storage | undefined, key: string, value: string): void {
  try {
    store?.setItem(key, value)
  } catch {
    // ignored — see safeGet
  }
}

function safeRemove(store: Storage | undefined, key: string): void {
  try {
    store?.removeItem(key)
  } catch {
    // ignored — see safeGet
  }
}

function local(): Storage | undefined {
  return typeof window === "undefined" ? undefined : window.localStorage
}

function session(): Storage | undefined {
  return typeof window === "undefined" ? undefined : window.sessionStorage
}

/**
 * localStorage (not sessionStorage) on purpose: a new tab is exactly the case
 * that has to keep working, and sessionStorage is per-tab.
 */
export function saveSessionHint(membershipId: string): void {
  safeSet(local(), SESSION_HINT_KEY, JSON.stringify({ membershipId }))
}

export function readSessionHint(): SessionHint | null {
  const raw = safeGet(local(), SESSION_HINT_KEY)
  if (!raw) return null

  try {
    const parsed = JSON.parse(raw) as Partial<SessionHint>
    if (typeof parsed.membershipId !== "string" || parsed.membershipId === "") return null
    return { membershipId: parsed.membershipId }
  } catch {
    return null
  }
}

export function clearSessionHint(): void {
  safeRemove(local(), SESSION_HINT_KEY)
}

/**
 * Accepts only same-origin absolute paths that can hold a session, so a
 * stashed value can neither become an open redirect (`//evil.com`,
 * `/\evil.com`) nor bounce the user back into the auth flow it just left.
 */
export function sanitizeReturnPath(path: string | null | undefined): string | null {
  if (!path || !path.startsWith("/")) return null
  if (path.startsWith("//") || path.startsWith("/\\")) return null

  const pathname = path.split(/[?#]/)[0]
  if (pathname === "/login" || pathname.startsWith("/login/")) return null
  if (pathname === "/auth" || pathname.startsWith("/auth/")) return null

  return path
}

export function saveReturnPath(path: string): void {
  const safe = sanitizeReturnPath(path)
  if (!safe) {
    safeRemove(session(), RETURN_PATH_KEY)
    return
  }
  safeSet(session(), RETURN_PATH_KEY, safe)
}

/** One-shot read: the path is cleared whether or not it survives validation. */
export function consumeReturnPath(): string | null {
  const raw = safeGet(session(), RETURN_PATH_KEY)
  safeRemove(session(), RETURN_PATH_KEY)
  return sanitizeReturnPath(raw)
}

export function markSilentAttempt(): void {
  safeSet(session(), SILENT_ATTEMPT_KEY, "1")
}

/** One-shot read — the callback must never act on a stale attempt flag. */
export function consumeSilentAttempt(): boolean {
  const raw = safeGet(session(), SILENT_ATTEMPT_KEY)
  safeRemove(session(), SILENT_ATTEMPT_KEY)
  return raw === "1"
}

export function clearSilentAttempt(): void {
  safeRemove(session(), SILENT_ATTEMPT_KEY)
}

/**
 * Re-runs the Authorization Code + PKCE flow as a top-level redirect with
 * `prompt=none`: Keycloak either answers with a code straight from the SSO
 * cookie (no user interaction) or with `error=login_required`, both handled
 * by the existing /auth/callback page.
 *
 * A top-level redirect rather than the usual hidden-iframe silent check-sso:
 * it reuses /auth/callback verbatim (including the membership -> CTX token ->
 * setSession bootstrap the UI depends on), needs no extra page, no
 * postMessage bridge and no registered silent-check redirect URI, and it does
 * not depend on Keycloak's frame-ancestors/X-Frame-Options defaults.
 */
export async function beginSilentSso(returnPath: string): Promise<void> {
  const verifier = generateCodeVerifier()
  const challenge = await generateCodeChallenge(verifier)
  const state = generateState()
  const nonce = generateNonce()

  savePkceParams({ verifier, state, nonce })
  saveReturnPath(returnPath)
  markSilentAttempt()

  // replace(), not assign(): the un-authenticated entry must not stay in
  // history, otherwise Back re-triggers the whole silent round trip.
  window.location.replace(
    buildAuthorizeUrl({
      redirectUri: callbackRedirectUri(),
      state,
      nonce,
      codeChallenge: challenge,
      prompt: "none",
    }),
  )
}
