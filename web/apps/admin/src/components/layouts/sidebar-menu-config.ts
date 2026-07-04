import {
  BarChart3,
  Boxes,
  Building2,
  ChefHat,
  ClipboardList,
  CreditCard,
  FileText,
  LayoutDashboard,
  Package,
  Settings,
  ShoppingBag,
  Table2,
  Tag,
  Users,
  UtensilsCrossed,
  Warehouse,
} from "lucide-react"

import type { MenuItem } from "@/lib/menu-utils"

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
  ]
}
