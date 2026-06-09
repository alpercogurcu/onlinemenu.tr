"use client"

import { Home } from "lucide-react"
import { useTranslations } from "next-intl"

import React from "react"

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

export default function DynamicBreadcrumb() {
  const t = useTranslations("navigation")
  const pathname = usePathname()

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
    const label =
      ROUTE_NAMES[segment] ||
      segment
        .split("-")
        .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
        .join(" ")
    const isCurrent = index === pathSegments.length - 1
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
