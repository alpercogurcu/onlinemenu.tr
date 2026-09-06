import { useTenant } from "@/hooks/use-tenant"

// Modules the running API actually mounts (backend/cmd/api/main.go). The
// tenant's `enabled_modules` is intersected with this list so the sidebar never
// advertises a screen whose backend is not wired yet (party, hr, billing today
// return 404 for every request). Update this list when a module is mounted.
export const MOUNTED_MODULES = ["pos", "catalog", "inventory", "storefront"] as const

export type ModuleKey = (typeof MOUNTED_MODULES)[number] | "party" | "billing" | "hr"

export function resolveEnabledModules(tenantModules: string[] | undefined): ModuleKey[] {
  // Before the tenant has loaded we show every mounted module rather than an
  // empty sidebar — flicker on first paint would be worse than a brief
  // over-inclusion, and nothing here is an authorization boundary.
  if (!tenantModules) return [...MOUNTED_MODULES]
  return MOUNTED_MODULES.filter((m) => tenantModules.includes(m))
}

export function useEnabledModules(tenantId: string): ModuleKey[] {
  const { data } = useTenant(tenantId)
  return resolveEnabledModules(data?.enabled_modules)
}
