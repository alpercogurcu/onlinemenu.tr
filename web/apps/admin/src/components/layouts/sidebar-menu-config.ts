import {
  BarChart3,
  Boxes,
  Building2,
  ChefHat,
  ClipboardList,
  CreditCard,
  FileText,
  LayoutDashboard,
  ListTree,
  Package,
  Receipt,
  Router,
  Settings,
  ShieldCheck,
  ShoppingBag,
  Store,
  Table2,
  Tag,
  Users,
  UtensilsCrossed,
  Warehouse,
} from "lucide-react"

import type { MenuItem } from "@/lib/menu-utils"
import type { ModuleKey } from "@/lib/modules"

// One labelled sidebar group. `module` names the backend module the group's
// screens talk to; a group without one (overview, business settings) is
// always shown. admin-sidebar.tsx drops groups whose module the tenant has
// not enabled — or that the API does not mount yet (see lib/modules.ts).
export interface SidebarSection {
  label: string
  module?: ModuleKey
  items: MenuItem[]
}

export function getOverviewMenuConfig(t: (key: string) => string): MenuItem[] {
  return [
    {
      title: t("navigation.dashboard"),
      url: "/",
      icon: LayoutDashboard,
    },
  ]
}

export function getPOSMenuConfig(t: (key: string) => string): MenuItem[] {
  return [
    {
      title: t("navigation.tables"),
      url: "/pos/tables",
      icon: Table2,
    },
    {
      title: t("navigation.checks"),
      url: "/pos/checks",
      icon: ClipboardList,
    },
    {
      title: t("navigation.kitchen"),
      url: "/pos/kitchen",
      icon: ChefHat,
    },
  ]
}

export function getCatalogMenuConfig(t: (key: string) => string): MenuItem[] {
  return [
    {
      title: t("navigation.products"),
      url: "/catalog/products",
      icon: ShoppingBag,
    },
    {
      title: t("navigation.categories"),
      url: "/catalog/categories",
      icon: Tag,
    },
    {
      title: t("navigation.modifiers"),
      url: "/catalog/modifiers",
      icon: UtensilsCrossed,
    },
    {
      title: t("navigation.menus"),
      url: "/catalog/menus",
      icon: FileText,
    },
    {
      title: t("navigation.branchPricing"),
      url: "/catalog/branch-pricing",
      icon: Store,
    },
  ]
}

export function getInventoryMenuConfig(
  t: (key: string) => string,
): MenuItem[] {
  return [
    {
      title: t("navigation.warehouses"),
      url: "/inventory/warehouses",
      icon: Warehouse,
    },
    {
      title: t("navigation.stockItems"),
      url: "/inventory/stock-items",
      icon: Boxes,
    },
    {
      title: t("navigation.stockLevels"),
      url: "/inventory/stock-levels",
      icon: Package,
    },
    {
      title: t("navigation.stockMovements"),
      url: "/inventory/movements",
      icon: BarChart3,
    },
    {
      title: t("navigation.supplyPolicies"),
      url: "/inventory/supply-policies",
      icon: ShieldCheck,
    },
    {
      title: t("navigation.purchaseReceipts"),
      url: "/inventory/purchase-receipts",
      icon: Receipt,
    },
  ]
}

export function getPartyMenuConfig(t: (key: string) => string): MenuItem[] {
  return [
    {
      title: t("navigation.customers"),
      url: "/parties/customers",
      icon: Users,
    },
  ]
}

export function getPaymentMenuConfig(t: (key: string) => string): MenuItem[] {
  return [
    {
      title: t("navigation.payments"),
      url: "/payment/payments",
      icon: CreditCard,
    },
  ]
}

export function getBillingMenuConfig(t: (key: string) => string): MenuItem[] {
  return [
    {
      title: t("navigation.invoices"),
      url: "/billing/invoices",
      icon: FileText,
    },
    {
      title: t("navigation.billingSettings"),
      url: "/billing/settings",
      icon: Settings,
    },
  ]
}

export function getHRMenuConfig(t: (key: string) => string): MenuItem[] {
  return [
    {
      title: t("navigation.employees"),
      url: "/hr/employees",
      icon: Users,
    },
  ]
}

export function getSettingsMenuConfig(
  t: (key: string) => string,
): MenuItem[] {
  return [
    {
      title: t("navigation.branches"),
      url: "/settings/branches",
      icon: Building2,
    },
    {
      title: t("navigation.users"),
      url: "/settings/users",
      icon: Users,
    },
    {
      title: t("navigation.roles"),
      url: "/settings/roles",
      icon: ClipboardList,
    },
    {
      title: t("navigation.generalSettings"),
      url: "/settings/general",
      icon: Settings,
    },
    {
      title: t("navigation.integrations"),
      url: "/settings/integrations",
      icon: Package,
    },
    {
      title: t("navigation.fiscalTerminals"),
      url: "/settings/fiscal-terminals",
      icon: Router,
    },
    {
      title: t("navigation.fiscalSections"),
      url: "/settings/fiscal-sections",
      icon: ListTree,
    },
  ]
}

// `canOpen` decides per item whether the current principal may open the
// screen. AdminSidebar passes lib/route-permissions' canAccessRoute, i.e. the
// same registry the layout's RouteGuard enforces, so the menu can never
// advertise a screen the guard (or the API) refuses. Sections left empty are
// dropped.
export function getSidebarSections(
  t: (key: string) => string,
  canOpen: (url: string) => boolean = () => true,
): SidebarSection[] {
  const sections: SidebarSection[] = [
    { label: t("navigation.general"), items: getOverviewMenuConfig(t) },
    { label: t("navigation.pos"), module: "pos", items: getPOSMenuConfig(t) },
    { label: t("navigation.catalog"), module: "catalog", items: getCatalogMenuConfig(t) },
    { label: t("navigation.inventory"), module: "inventory", items: getInventoryMenuConfig(t) },
    { label: t("navigation.parties"), module: "party", items: getPartyMenuConfig(t) },
    // Payments are served by the pos module (payment/public is consumed
    // through pos), so they follow the pos flag rather than their own.
    { label: t("navigation.payment"), module: "pos", items: getPaymentMenuConfig(t) },
    { label: t("navigation.billing"), module: "billing", items: getBillingMenuConfig(t) },
    { label: t("navigation.hr"), module: "hr", items: getHRMenuConfig(t) },
    { label: t("navigation.business"), items: getSettingsMenuConfig(t) },
  ]
  return sections
    .map((section) => ({ ...section, items: section.items.filter((item) => canOpen(item.url)) }))
    .filter((section) => section.items.length > 0)
}
