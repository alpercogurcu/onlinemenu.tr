package authz

import future.keywords.if
import future.keywords.in

# Authorization layer 2 of 4 (ADR-AUTH-001):
# RLS (layer 1) → OPA Allow + Scope (layer 2) → Service WHERE clause (layer 3) → DTO projection (layer 4)
#
# This policy returns:
#   allow: bool   — whether the action is permitted at all
#   scope: string — data visibility level ("tenant" | "branch" | "own")
#
# OPA does NOT return permission lists; field-level filtering is done in DTO projection only.

default allow = false
default scope = "own"

# ── Tenant admin ────────────────────────────────────────────────────────────────
# A tenant admin can perform any action within their tenant.
allow if {
	"admin" in input.principal.roles
}

scope := "tenant" if {
	"admin" in input.principal.roles
}

# ── Branch manager ───────────────────────────────────────────────────────────────
# A manager can perform actions within the branches they are assigned to.
allow if {
	"manager" in input.principal.roles
	not "admin" in input.principal.roles
}

scope := "branch" if {
	"manager" in input.principal.roles
	not "admin" in input.principal.roles
}

# ── Cashier ──────────────────────────────────────────────────────────────────────
# Cashiers can perform POS sale operations within their branch.
allow if {
	"cashier" in input.principal.roles
	input.action in {"pos:order:create", "pos:order:read", "pos:payment:create"}
}

# ── Waiter ───────────────────────────────────────────────────────────────────────
# Waiters can create and read orders; they cannot process payments.
allow if {
	"waiter" in input.principal.roles
	input.action in {"pos:order:create", "pos:order:read", "pos:table:read"}
}

# ── Kitchen staff ────────────────────────────────────────────────────────────────
# Kitchen display system users can only read and update order item status.
allow if {
	"kitchen" in input.principal.roles
	input.action in {"pos:order:read", "pos:order:item:update_status"}
}

# ── Inventory read ──────────────────────────────────────────────────────────────
# Kitchen and bar staff can read stock levels (their DB role_permissions have inventory:read).
# Shift manager can read and adjust stock within their branch.
allow if {
	input.principal.roles[_] in {"kitchen", "bar"}
	input.action in {"inventory:level:read", "inventory:transaction:read"}
}

allow if {
	"shift_manager" in input.principal.roles
	input.action in {"inventory:level:read", "inventory:transaction:read",
	                 "inventory:transaction:create"}
}

# ── Read-only scope for non-manager roles ────────────────────────────────────────
# Non-admin, non-manager roles see only their own branch data.
# The service layer is responsible for translating "own" scope into a WHERE branch_id = ?
# clause using the principal's branch_id from the JWT.
scope := "own" if {
	not "admin" in input.principal.roles
	not "manager" in input.principal.roles
}
