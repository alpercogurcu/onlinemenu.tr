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
	# "warehouse" is forward-declared here (ADR-DATA-005 İlke 4: depo/imalat
	# şubesinin manager/warehouse rolü inventory permission'ı alır) but is NOT
	# yet seeded in identity/000006_seed_system_roles.up.sql — that migration
	# is outside this task's file scope (backend/internal/modules/identity is
	# not touched here). Until the identity module seeds role id ...0007 with
	# system_key='warehouse', no principal can actually hold this role, so the
	# rules below are inert-but-correct: they take effect the moment identity
	# seeds the role, with no further rego change required. Flagged as a
	# required identity-module follow-up in the sprint report.
	"warehouse": "00000001-0000-0000-0000-000000000007",
	# "waiter" (garson) is forward-declared for the same reason as
	# "warehouse" above (ADR-DATA-006 masa planı: pos.table.read must include
	# waiter per the sprint spec) but is likewise NOT yet seeded in
	# identity/000006_seed_system_roles.up.sql — db-schema.md's BRANCH_USERS
	# role enum already lists it, seeding it is an identity-module follow-up
	# outside this task's file scope. Inert until seeded, takes effect with no
	# further rego change once it is.
	"waiter": "00000001-0000-0000-0000-000000000008",
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

# -- Inventory (legacy, pre-ADR-DATA-005 branch-scoped model): ORPHANED.
# inventory_levels/inventory_transactions were re-keyed to warehouse-scoped
# stock_levels/stock_movements (migrations/inventory/000003); no live endpoint
# is wired to "inventory.transaction.*" any more (the HTTP layer now uses
# "inventory.level.read" and "inventory.movement.*", both governed by
# inventory_management_actions below, manager/warehouse only per ADR-DATA-005
# İlke 4). This block is left in place, unused, only because
# internal/platform/auth/opa_test.go asserts it directly by action string
# (TestEngine_Decide_ShiftManager_InventoryWrite) and that file is outside
# this task's scope to edit — removing it would break that test without a
# corresponding code change to fix it. Do not wire any new endpoint to
# "inventory.transaction.*"; it is dead policy, kept only for that test.
allow if {
	input.action == "inventory.transaction.read"
	any_role({"kitchen", "bar", "shift_manager"})
}

allow if {
	input.action == "inventory.transaction.create"
	has_role("shift_manager")
}

# -- Inventory management (ADR-DATA-005 İlke 4): stock items, warehouses,
# stock movements, branch transfer orders and shipments are manager/warehouse
# only. Counter-facing roles (cashier/waiter) and kitchen/bar get NONE of
# these actions — visibility is route/permission absence, never a row-level
# opt-in flag (this is exactly the discipline the b2b post-mortem in
# ADR-DATA-005 says was missing: "BranchStockTracking" was a per-row bayrak,
# not a module boundary).
inventory_management_actions := {
	"inventory.level.read",
	"inventory.stock_item.read",
	"inventory.stock_item.create",
	"inventory.stock_item.update",
	"inventory.stock_item.delete",
	"inventory.warehouse.read",
	"inventory.warehouse.create",
	"inventory.warehouse.update",
	"inventory.warehouse.delete",
	"inventory.movement.read",
	"inventory.movement.create",
	"inventory.transfer_order.read",
	"inventory.transfer_order.create",
	"inventory.transfer_order.submit",
	"inventory.transfer_order.approve",
	"inventory.transfer_order.reject",
	"inventory.transfer_order.cancel",
	"inventory.transfer_order.fulfil",
	"inventory.shipment.read",
	"inventory.shipment.create",
	"inventory.shipment.advance",
	"inventory.shipment.receive",
	"inventory.shipment.cancel",
	# ADR-DATA-007: supply policy READ is manager+warehouse, same as every
	# other inventory management resource. CREATE is deliberately NOT in this
	# set — see the standalone rule below: it stays manager-only (via the
	# wildcard at the top of this file), reflecting that the commercial
	# procurement contract itself (exclusive_hq / approved_suppliers / free)
	# is a franchisor-level decision, not an operational depo/warehouse task.
	"inventory.supply_policy.read",
	# ADR-DATA-007 karar 3: purchase_receipt (elden fiş) create+read are both
	# manager+warehouse, same as shipments/BTO — a depo/warehouse operator
	# physically receiving a market/pazar purchase must be able to record it,
	# unlike supply_policy.create (the commercial contract) which stays
	# manager-only above.
	"inventory.purchase_receipt.create",
	"inventory.purchase_receipt.read",
}

allow if {
	input.action in inventory_management_actions
	has_role("warehouse")
}

# -- Supply policy CREATE (ADR-DATA-007): manager-only. Deliberately absent
# from inventory_management_actions above so that adding it there in the
# future cannot silently also grant it to "warehouse" — see that set's
# comment. Manager already gets this via the wildcard `allow if
# has_role("manager")` at the top of this file; this comment exists so the
# absence of a "warehouse" rule here is legible as an explicit choice, not
# an oversight.

# -- POS: check lifecycle (open/close/cancel) and order intake (place/accept/
# reject) are counter-facing actions, owned by cashier/shift_manager (mirrors
# role_permissions seed: checks/orders create+read+update for both).
pos_counter_actions := {
	"pos.check.read",
	"pos.check.open",
	"pos.check.close",
	"pos.check.cancel",
	"pos.order.read",
	"pos.order.place",
	"pos.order.accept",
	"pos.order.reject",
	"pos.order.advance",
}

allow if {
	input.action in pos_counter_actions
	any_role({"cashier", "shift_manager"})
}

# -- Kitchen/bar: read tickets and advance them through preparing/ready; they
# never open/close checks or accept/reject intake — that stays with the
# counter roles above (mirrors role_permissions seed: orders read+update only).
pos_kitchen_actions := {
	"pos.order.read",
	"pos.order.advance",
}

allow if {
	input.action in pos_kitchen_actions
	any_role({"kitchen", "bar"})
}

# -- POS: table plan (Sprint-5 Wave 1, docs/db-schema.md TABLE_ZONES/TABLES).
# Reading the floor plan (pos.table.read) is open to every branch-facing role
# that needs to see table state — cashier/shift_manager at the counter,
# waiter serving tables, kitchen/bar checking which table a ticket belongs
# to. Managing it (zone/table CRUD, manual status changes) stays with
# management roles only, mirroring pos_counter_actions' cashier/
# shift_manager split for check lifecycle.
pos_table_read_actions := {"pos.table.read"}

allow if {
	input.action in pos_table_read_actions
	any_role({"cashier", "shift_manager", "waiter", "kitchen", "bar"})
}

pos_table_manage_actions := {"pos.table.manage"}

allow if {
	input.action in pos_table_manage_actions
	has_role("shift_manager")
}

# -- Payment: cashier/shift_manager register sales at the counter (mirrors
# role_permissions seed: payment.create for both). Listing/reading past
# payments is reserved for shift reconciliation (shift_manager) and manager
# (wildcard, covered above).
allow if {
	input.action == "payment.sale.register"
	any_role({"cashier", "shift_manager"})
}

allow if {
	input.action == "payment.payment.read"
	has_role("shift_manager")
}

# -- Payment: fiscal registration status polling. A branch runs several POS
# stations; a sale started on one station holds a pending fiscal submission
# the other stations cannot see, so they must poll for it before closing a
# check. That is a narrow, branch-scoped read (payment id, amount, age,
# terminal outcome) — deliberately NOT payment.payment.read, which exposes the
# full payment history and stays a reconciliation permission. Cashier needs it
# because the cashier is the one holding the check open at the counter.
allow if {
	input.action == "payment.fiscal_status.read"
	any_role({"cashier", "shift_manager"})
}

# -- Scope resolution for non-manager allows above: branch-scoped, since cashier/
# shift_manager/kitchen/bar operate within a single branch (Principal.BranchID).
scope := "branch" if {
	not has_role("manager")
	any_role({"cashier", "shift_manager", "driver", "kitchen", "bar", "warehouse", "waiter"})
}
