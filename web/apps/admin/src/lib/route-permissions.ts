// Single source of truth for "which action opens which screen".
//
// Every app/(main)/**/page.tsx has exactly one entry here (enforced by
// src/test/route-permissions.test.ts — the frontend mirror of the backend's
// route_guard_coverage_test.go). The layout's RouteGuard and the sidebar both
// derive from this map, so a screen can no longer be linked in the menu yet
// open to a role that the API answers 403 for, or vice versa.
//
// Choosing the action: back-office editors are gated on a manager-only
// WRITE/management action on purpose. Several READ grants are deliberately
// wide for operational reasons — catalog reads reach cashier/waiter/kitchen/bar
// because taking an order means browsing products, tenant.branch.read reaches
// every role because branch-keyed screens need the branch list. Gating the
// catalog editor on catalog.product.read would hand a garson a product editor
// whose every save fails.
import { currentRoleNames, rolesCan } from "@/lib/permissions"

export const ROUTE_PERMISSIONS = {
  // Dashboard: day-end figures (shift_manager + manager).
  "/": "pos.report.read",

  "/pos/tables": "pos.table.read",
  "/pos/checks": "pos.check.read",
  "/pos/checks/[id]": "pos.check.read",
  // The kitchen display exists to move tickets along; a waiter reads orders
  // (pos.order.read) for their own adisyon but does not run the KDS.
  "/pos/kitchen": "pos.order.advance",

  "/catalog/products": "catalog.product.update",
  "/catalog/products/new": "catalog.product.create",
  "/catalog/products/[id]": "catalog.product.update",
  "/catalog/categories": "catalog.category.create",
  "/catalog/modifiers": "catalog.modifier_group.update",
  "/catalog/modifiers/new": "catalog.modifier_group.create",
  "/catalog/modifiers/[id]": "catalog.modifier_group.update",
  "/catalog/menus": "catalog.menu.update",
  "/catalog/menus/[id]": "catalog.menu.update",
  "/catalog/branch-pricing": "catalog.branch_override.manage",

  "/inventory/warehouses": "inventory.warehouse.read",
  "/inventory/stock-items": "inventory.stock_item.read",
  "/inventory/stock-levels": "inventory.level.read",
  "/inventory/movements": "inventory.movement.read",
  "/inventory/supply-policies": "inventory.supply_policy.read",
  "/inventory/purchase-receipts": "inventory.purchase_receipt.read",

  "/parties": "party.party.read",
  "/parties/customers": "party.party.read",

  "/payment/payments": "payment.payment.read",

  "/billing/invoices": "billing.invoice.read",
  "/billing/settings": "billing.invoice.create",

  "/hr/employees": "hrcore.employee.read",

  // tenant.branch.read is granted to every role (see header); branch
  // administration is gated on the manager-only create action instead.
  "/settings/branches": "tenant.branch.create",
  "/settings/users": "identity.membership.read",
  "/settings/roles": "identity.role.read",
  "/settings/general": "tenant.tenant.read",
  "/settings/integrations": "tenant.integrator.read",
  "/settings/fiscal-terminals": "payment.fiscal_terminal.read",
  "/settings/fiscal-sections": "payment.fiscal_section_mapping.read",
} as const satisfies Record<string, string>

export type RoutePattern = keyof typeof ROUTE_PERMISSIONS

interface CompiledRoute {
  pattern: RoutePattern
  regex: RegExp
  dynamicSegments: number
}

// Static segments win over dynamic ones ("/catalog/products/new" must not be
// read as "/catalog/products/[id]"), so fewer dynamic segments sort first.
const COMPILED: CompiledRoute[] = (Object.keys(ROUTE_PERMISSIONS) as RoutePattern[])
  .map((pattern) => ({
    pattern,
    regex: new RegExp(
      "^" + pattern.replace(/[.*+?^${}()|\\]/g, "\\$&").replace(/\[[^\]/]+\]/g, "[^/]+") + "/?$",
    ),
    dynamicSegments: (pattern.match(/\[/g) ?? []).length,
  }))
  .sort((a, b) => a.dynamicSegments - b.dynamicSegments)

/** Registry pattern for a concrete pathname, or null when none matches. */
export function matchRoute(pathname: string): RoutePattern | null {
  const clean = pathname.split(/[?#]/)[0] || "/"
  for (const route of COMPILED) {
    if (route.regex.test(clean)) return route.pattern
  }
  return null
}

/** Action a pathname requires, or null for a path outside the registry. */
export function requiredAction(pathname: string): string | null {
  const pattern = matchRoute(pathname)
  return pattern ? ROUTE_PERMISSIONS[pattern] : null
}

/** Fails closed: an unregistered path is not accessible. */
export function rolesCanAccess(roles: Iterable<string>, pathname: string): boolean {
  const action = requiredAction(pathname)
  return action !== null && rolesCan(roles, action)
}

export function canAccessRoute(pathname: string): boolean {
  return rolesCanAccess(currentRoleNames(), pathname)
}

// Landing screen per role, most specific operational role first. A principal
// holding several roles lands on the first one listed that it can open.
// "/pos/order" is reserved for the web order screen (separate work item); the
// waiter lands on the table plan until it exists.
const ROLE_HOMES: ReadonlyArray<readonly [role: string, path: string]> = [
  ["manager", "/"],
  ["shift_manager", "/"],
  ["cashier", "/pos/checks"],
  ["waiter", "/pos/tables"],
  ["kitchen", "/pos/kitchen"],
  ["bar", "/pos/kitchen"],
  ["warehouse", "/inventory/stock-levels"],
]

/**
 * Where `roles` should land after sign-in (and when they open "/" without
 * dashboard access). Falls back to the first registered screen they may open;
 * null when there is none (e.g. driver today).
 */
export function homeRouteFor(roles: Iterable<string>): string | null {
  const held = new Set(roles)
  for (const [role, path] of ROLE_HOMES) {
    if (held.has(role) && rolesCanAccess(held, path)) return path
  }
  for (const pattern of Object.keys(ROUTE_PERMISSIONS) as RoutePattern[]) {
    if (!pattern.includes("[") && rolesCanAccess(held, pattern)) return pattern
  }
  return null
}

export function currentHomeRoute(): string | null {
  return homeRouteFor(currentRoleNames())
}
