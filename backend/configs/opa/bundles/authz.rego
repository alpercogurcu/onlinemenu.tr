package authz

import future.keywords.if
import future.keywords.in

# Authorization layer 2 of 4 (ADR-AUTH-001):
# RLS (layer 1) -> OPA Allow + Scope (layer 2) -> Service WHERE clause (layer 3) -> DTO projection (layer 4)
#
# This policy returns:
#   allow: bool   - whether the action is permitted at all
#   scope: string - data visibility level ("tenant" | "branch" | "own")
#
# OPA does NOT return permission lists; field-level filtering is done in DTO projection only.
#
# Action naming: "<module>.<resource>.<verb>", e.g. "catalog.product.create".
#
# Role representation: input.principal.roles carries role UUIDs (Principal.RoleIDs),
# not names. This policy matches against the well-known *system* role IDs seeded in
# identity/000006_seed_system_roles.up.sql (immutable templates, tenant_id IS NULL).
# Tenant-specific custom roles are NOT resolved here: doing so requires a role_id ->
# system_key lookup backed by identity module internals (PermissionRepo / PermSet),
# which is out of platform/auth's module boundary. Custom-role scoped policies are
# deferred to a Faz 2 follow-up (see docs/lessons-from-b2b.md task list, item 1).
system_roles := {
	"cashier": "00000001-0000-0000-0000-000000000001",
	"shift_manager": "00000001-0000-0000-0000-000000000002",
	"driver": "00000001-0000-0000-0000-000000000003",
	"kitchen": "00000001-0000-0000-0000-000000000004",
	"bar": "00000001-0000-0000-0000-000000000005",
	"manager": "00000001-0000-0000-0000-000000000006",
}

has_role(name) if {
	system_roles[name] in input.principal.roles
}

any_role(names) if {
	some name in names
	has_role(name)
}

default allow = false

default scope = "own"

# -- Manager: chain-wide administrator (seed migration comment: "wildcard - tum kaynak
# ve eylemler"). Manager is the only system role authorized for back-office modules
# (tenant configuration, identity role/membership management, party/CRM, hr-core,
# billing) in Faz 1 — those modules have no seeded permission rows for other roles.
allow if {
	has_role("manager")
}

scope := "tenant" if {
	has_role("manager")
}

# -- Catalog: read access for POS-facing roles; writes remain manager-only (covered
# by the manager rule above).
catalog_read_actions := {
	"catalog.category.read",
	"catalog.product.read",
	"catalog.modifier_group.read",
	"catalog.modifier.read",
	"catalog.menu.read",
	"catalog.menu_item.read",
}

allow if {
	input.action in catalog_read_actions
	any_role({"cashier", "shift_manager", "kitchen", "bar"})
}

# -- Inventory: kitchen/bar/shift_manager may read stock levels and transactions;
# shift_manager may additionally record adjustments (mirrors the pre-existing
# role_permissions seed intent for stock corrections during a shift).
inventory_read_actions := {
	"inventory.level.read",
	"inventory.transaction.read",
}

allow if {
	input.action in inventory_read_actions
	any_role({"kitchen", "bar", "shift_manager"})
}

allow if {
	input.action == "inventory.transaction.create"
	has_role("shift_manager")
}

# -- Scope resolution for non-manager allows above: branch-scoped, since cashier/
# shift_manager/kitchen/bar operate within a single branch (Principal.BranchID).
scope := "branch" if {
	not has_role("manager")
	any_role({"cashier", "shift_manager", "driver", "kitchen", "bar"})
}
