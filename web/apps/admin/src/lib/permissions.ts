// Cosmetic-only, client-side permission gating.
//
// ADR-AUTH-001 is explicit: OPA does NOT return a permission list, and
// field-level filtering happens in DTO projection only. This module does
// neither — it does not ask the backend "what am I allowed to do" (that
// endpoint does not exist and must not be built for this purpose). Instead
// it decodes the `rids` (role ids) claim already embedded in the CTX token
// this browser tab is holding in memory (see lib/api.ts) and matches it
// against a small, hand-maintained mirror of authz.rego's role-gated `allow`
// rules — purely so the UI can hide/disable a control the backend would 403
// on anyway.
//
// This is NOT an authorization boundary. RLS -> OPA -> service WHERE clause
// -> DTO projection (the real 4 layers) all still run on every request
// regardless of what this file decides. A user who spoofs or bypasses this
// check gains nothing: the request still hits the same backend checks any
// other request would.
import { getAccessToken } from "@/lib/api"
import { decodeJwtPayload } from "@/lib/jwt"

// Well-known system role ids — immutable templates (tenant_id IS NULL)
// seeded by identity/000006_seed_system_roles.up.sql and matched by name in
// backend/configs/opa/bundles/authz.rego's `system_roles` map. Safe to
// hardcode here because these ids are the same for every tenant; a
// tenant-specific custom role would NOT be safe to hardcode (authz.rego's
// own header comment flags custom-role policy resolution as a Faz 2
// follow-up for the same reason).
const SYSTEM_ROLE_NAMES_BY_ID: Record<string, string> = {
  "00000001-0000-0000-0000-000000000001": "cashier",
  "00000001-0000-0000-0000-000000000002": "shift_manager",
  "00000001-0000-0000-0000-000000000003": "driver",
  "00000001-0000-0000-0000-000000000004": "kitchen",
  "00000001-0000-0000-0000-000000000005": "bar",
  "00000001-0000-0000-0000-000000000006": "manager",
  "00000001-0000-0000-0000-000000000007": "warehouse",
  "00000001-0000-0000-0000-000000000008": "waiter",
}

interface CtxTokenClaims {
  rids?: string[]
}

// Decodes (without verifying — see module header) the role ids the CTX token
// carries for the CURRENT tenant/branch context (IssueStaff scopes `rids` at
// mint time; see context_token.go), and maps them to the well-known role
// names above. Unknown/custom role ids resolve to nothing, which is the
// correct fail-closed direction for a cosmetic check: an unrecognized role
// simply gets no bonus visibility.
function currentRoleNames(): Set<string> {
  const token = getAccessToken()
  if (!token) return new Set()

  const claims = decodeJwtPayload<CtxTokenClaims>(token)
  const roleIds = claims?.rids ?? []

  const names = new Set<string>()
  for (const id of roleIds) {
    const name = SYSTEM_ROLE_NAMES_BY_ID[id]
    if (name) names.add(name)
  }
  return names
}

// "manager" holds the chain-wide wildcard in authz.rego (`allow if
// has_role("manager")`, top of the file) — implicitly allowed for every
// gated action below, regardless of ACTION_ROLES' explicit list.
const WILDCARD_ROLE = "manager"

// Action -> role names allowed to perform it, hand-mirrored from the
// matching `allow if { input.action in ...; any_role/has_role(...) }` block
// in backend/configs/opa/bundles/authz.rego. There is no shared source of
// truth between the Go/rego policy and this table — keep them in sync by
// hand, and only add entries for actions the admin app actually gates
// client-side (do not try to mirror the whole policy file here).
const ACTION_ROLES: Record<string, ReadonlySet<string>> = {
  // authz.rego storefront_qr_read_actions: viewing which table already has a
  // live code. Granted to cashier + shift_manager (+ manager wildcard) —
  // deliberately NOT kitchen/bar, even though pos_table_read_actions grants
  // those roles pos.table.read (they can see the floor plan but not QR
  // state). Without this entry a kitchen/bar user reaching /pos/tables could
  // open the QR dialog and hit a silent 403 on GET /qr-codes.
  "storefront.qr.read": new Set(["cashier", "shift_manager"]),
  // authz.rego storefront_qr_manage_actions: create/rotate/revoke are one
  // action (storefront.qr.manage), granted to shift_manager (+ manager
  // wildcard).
  "storefront.qr.manage": new Set(["shift_manager"]),
  // GET /pos/reports/sale-details: end-of-day sales report, granted to
  // shift_manager (+ manager wildcard) — deliberately NOT cashier, who runs
  // the till but does not see the branch's aggregate figures. The backend
  // 403s a cashier regardless; this only hides the report section instead of
  // rendering a raw error after a fetch that was always going to fail.
  "pos.report.read": new Set(["shift_manager"]),
}

/**
 * Cosmetic permission check — see module header. An action absent from
 * ACTION_ROLES is allowed by default, so this helper only ever narrows
 * visibility for actions explicitly wired here; it never grants anything
 * the backend wouldn't have granted anyway.
 */
export function can(action: string): boolean {
  const allowed = ACTION_ROLES[action]
  if (!allowed) return true

  const roles = currentRoleNames()
  if (roles.has(WILDCARD_ROLE)) return true

  for (const role of roles) {
    if (allowed.has(role)) return true
  }
  return false
}
