"use client"

import { Home } from "lucide-react"
import { useTranslations } from "next-intl"

import React, { useEffect, useSyncExternalStore } from "react"

import Link from "next/link"
import { usePathname } from "next/navigation"

import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"

// A tiny external store (no Provider needed, so it works without touching
// the layout that mounts DynamicBreadcrumb) letting a detail page register
// the human label for its own last breadcrumb segment — e.g. the product
// editor swaps the raw product UUID for "Adana Kebap". Only one label is
// ever live at a time, keyed by the pathname it was set for, so navigating
// away — or a route whose page never calls the hook — falls back to the
// default segment-name rendering below.
let currentLabel: { path: string; label: string } | null = null
const labelListeners = new Set<() => void>()

function notifyLabelListeners() {
  labelListeners.forEach((listener) => listener())
}

function subscribeToLabel(listener: () => void) {
  labelListeners.add(listener)
  return () => {
    labelListeners.delete(listener)
  }
}

function getLabelSnapshot() {
  return currentLabel
}

function getServerLabelSnapshot() {
  return null
}

// Call from a detail page (e.g. product-editor.tsx) with the entity's own
// name once it is known; pass undefined while it is still loading or for a
// "new" route. Exported so the modifier group editor can wire the same
// mechanism for its own detail page.
export function useBreadcrumbLabel(label: string | undefined) {
  const pathname = usePathname()
  useEffect(() => {
    if (!label) return undefined
    currentLabel = { path: pathname, label }
    notifyLabelListeners()
    return () => {
      if (currentLabel?.path === pathname) {
        currentLabel = null
        notifyLabelListeners()
      }
    }
  }, [pathname, label])
}

export default function DynamicBreadcrumb() {
  const t = useTranslations("navigation")
  const pathname = usePathname()
  const labelEntry = useSyncExternalStore(subscribeToLabel, getLabelSnapshot, getServerLabelSnapshot)

  // Segment names for a "new" entity route ("/catalog/products/new",
  // "/catalog/modifiers/new") — keyed by the segment right before "new",
  // since the generic Title-Case fallback below would otherwise render the
  // literal word "New".
  const NEW_SEGMENT_NAMES: { [key: string]: string } = {
    products: t("newProduct"),
    modifiers: t("newGroup"),
  }

  const ROUTE_NAMES: { [key: string]: string } = {
    dashboard: t("dashboard"),
    pos: t("pos"),
    tables: t("tables"),
    checks: t("checks"),
    kitchen: t("kitchen"),
    catalog: t("catalog"),
    products: t("products"),
    categories: t("categories"),
    modifiers: t("modifiers"),
    menus: t("menus"),
    inventory: t("inventory"),
    warehouses: t("warehouses"),
    "stock-levels": t("stockLevels"),
    movements: t("stockMovements"),
    payment: t("payment"),
    payments: t("payments"),
    billing: t("billing"),
    invoices: t("invoices"),
    settings: t("settings"),
    branches: t("branches"),
    users: t("users"),
    roles: t("roles"),
    general: t("generalSettings"),
    integrations: t("integrations"),
    parties: t("parties"),
    customers: t("customers"),
    hr: t("hr"),
    employees: t("employees"),
  }

  const pathSegments = pathname
    .split("/")
    .filter((segment) => segment !== "")
    .slice(0, 3)

  const breadcrumbItems = pathSegments.map((segment, index) => {
    const href = `/${pathSegments.slice(0, index + 1).join("/")}`
    const isCurrent = index === pathSegments.length - 1
    const titleCased = segment
      .split("-")
      .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
      .join(" ")

    let label: string
    if (segment === "new") {
      label = NEW_SEGMENT_NAMES[pathSegments[index - 1]] || titleCased
    } else if (isCurrent && labelEntry && labelEntry.path === pathname) {
      // A detail page (product/group editor) registered its own entity name
      // for the current route's last segment — use it instead of the raw
      // UUID route param.
      label = labelEntry.label
    } else {
      label = ROUTE_NAMES[segment] || titleCased
    }
    return { href, label, isCurrent }
  })

  if (pathSegments.length === 0) return null

  return (
    <Breadcrumb className="hidden md:block">
      <BreadcrumbList>
        <BreadcrumbItem className="hidden md:block">
          <BreadcrumbLink asChild>
            <Link href="/">
              <Home className="mr-2 h-4 w-4" />
            </Link>
          </BreadcrumbLink>
        </BreadcrumbItem>

        {breadcrumbItems.map((item) => (
          <React.Fragment key={item.href}>
            <BreadcrumbSeparator />
            <BreadcrumbItem>
              {item.isCurrent ? (
                <BreadcrumbPage>{item.label}</BreadcrumbPage>
              ) : (
                <BreadcrumbLink asChild>
                  <Link href={item.href}>{item.label}</Link>
                </BreadcrumbLink>
              )}
            </BreadcrumbItem>
          </React.Fragment>
        ))}
      </BreadcrumbList>
    </Breadcrumb>
  )
}
