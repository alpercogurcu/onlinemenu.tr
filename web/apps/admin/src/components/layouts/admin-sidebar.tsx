"use client"

import { useTranslations } from "next-intl"

import * as React from "react"

import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarRail,
} from "@/components/ui/sidebar"

import { MenuGenerator } from "./menu-generator"
import NavProfile from "./nav-profile"
import {
  getBillingMenuConfig,
  getCatalogMenuConfig,
  getHRMenuConfig,
  getInventoryMenuConfig,
  getOverviewMenuConfig,
  getPOSMenuConfig,
  getPartyMenuConfig,
  getPaymentMenuConfig,
  getSettingsMenuConfig,
} from "./sidebar-menu-config"

export default function AdminSidebar({
  ...props
}: React.ComponentProps<typeof Sidebar>) {
  const t = useTranslations()

  const tFn = (key: string) => t(key as Parameters<typeof t>[0])

  return (
    <Sidebar variant="inset" {...props}>
      <SidebarHeader>
        <div className="flex items-center justify-center py-2">
          <span className="text-xl font-bold text-primary tracking-tight">
            OnlineMenu
          </span>
        </div>
      </SidebarHeader>
      <SidebarContent>
        <MenuGenerator
          items={getOverviewMenuConfig(tFn)}
          groupLabel="Genel"
        />
        <MenuGenerator
          items={getPOSMenuConfig(tFn)}
          groupLabel="POS"
        />
        <MenuGenerator
          items={getCatalogMenuConfig(tFn)}
          groupLabel="Katalog"
        />
        <MenuGenerator
          items={getInventoryMenuConfig(tFn)}
          groupLabel="Stok"
        />
        <MenuGenerator
          items={getPartyMenuConfig(tFn)}
          groupLabel="Müşteriler"
        />
        <MenuGenerator
          items={getPaymentMenuConfig(tFn)}
          groupLabel="Ödeme"
        />
        <MenuGenerator
          items={getBillingMenuConfig(tFn)}
          groupLabel="Fatura"
        />
        <MenuGenerator
          items={getHRMenuConfig(tFn)}
          groupLabel="Personel"
        />
        <MenuGenerator
          items={getSettingsMenuConfig(tFn)}
          groupLabel="İşletme"
        />
      </SidebarContent>
      <SidebarFooter>
        <NavProfile />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
