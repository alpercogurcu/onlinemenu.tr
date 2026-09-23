// Cosmetic-only, client-side permission gating.
//
// ADR-AUTH-001 is explicit: OPA does NOT return a permission list, and
// field-level filtering happens in DTO projection only. This module does
// neither — it does not ask the backend "what am I allowed to do" (that
// endpoint does not exist and must not be built for this purpose). Instead it
// decodes the `rids` (role ids) claim already embedded in the CTX token this
// browser tab holds in memory (see lib/api.ts) and looks those roles up in
// generated/permission-matrix.json.
//
// That matrix is a BUILD artefact: backend/internal/platform/auth/
// permission_matrix_test.go evaluates the real compiled authz.rego for every
// system role x every known action and writes the file (`task authz:gen`); the
// same test fails CI when the committed copy is stale. There is no hand-kept
// mirror of the policy here any more (docs/lessons-from-b2b.md §4).
//
// This is NOT an authorization boundary. RLS -> OPA -> service WHERE clause
// -> DTO projection still run on every request regardless of what this file
// decides. It only keeps the UI from offering what the backend would refuse.
import matrix from "@/generated/permission-matrix.json"
import { getAccessToken } from "@/lib/api"
import { decodeJwtPayload } from "@/lib/jwt"

// Well-known system role ids (tenant_id IS NULL templates) -> system_key.
// Tenant role clones and custom roles resolve to nothing: authz.rego matches
// the same template ids only, so an unrecognized id gets nothing here either.
const SYSTEM_ROLE_NAMES_BY_ID: Readonly<Record<string, string>> = matrix.roles

const ACTION_ROLES: Readonly<Record<string, readonly string[]>> = matrix.actions

interface CtxTokenClaims {
  rids?: string[]
  // Branch the membership is scoped to; the nil UUID means chain-wide.
  bid?: string
}

const NIL_UUID = "00000000-0000-0000-0000-000000000000"

/**
 * Branch id the current CTX token is scoped to, or null for a chain-wide
 * principal (manager) or when no session is present. Branch-keyed screens
 * (table plan, kitchen display) use it to default to the operator's own
 * branch instead of whichever branch happens to be listed first.
 */
export function currentBranchId(): string | null {
  const token = getAccessToken()
  if (!token) return null
  const bid = decodeJwtPayload<CtxTokenClaims>(token)?.bid
  if (!bid || bid === NIL_UUID) return null
  return bid
}

/**
 * System role names carried by the current CTX token (IssueStaff scopes
 * `rids` to the selected tenant/branch at mint time). Empty without a session.
 */
export function currentRoleNames(): Set<string> {
  const token = getAccessToken()
  if (!token) return new Set()

  const roleIds = decodeJwtPayload<CtxTokenClaims>(token)?.rids ?? []
  const names = new Set<string>()
  for (const id of roleIds) {
    const name = SYSTEM_ROLE_NAMES_BY_ID[id]
    if (name) names.add(name)
  }
  return names
}

/** True when the action exists in the generated matrix (typo guard). */
export function isKnownAction(action: string): boolean {
  return Object.prototype.hasOwnProperty.call(ACTION_ROLES, action)
}

/** Pure lookup: may a principal holding `roles` perform `action`? */
export function rolesCan(roles: Iterable<string>, action: string): boolean {
  const allowed = ACTION_ROLES[action]
  if (!allowed) return false
  for (const role of roles) {
    if (allowed.includes(role)) return true
  }
  return false
}

/**
 * Cosmetic permission check — see module header. Fails closed: no session,
 * an unrecognized role or an action the matrix does not know all answer false.
 */
export function can(action: string): boolean {
  return rolesCan(currentRoleNames(), action)
}
