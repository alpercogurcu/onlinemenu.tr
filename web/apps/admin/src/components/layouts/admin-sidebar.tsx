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
import { useEnabledModules } from "@/lib/modules"
import { useAuthStore } from "@/store/auth-store"

import { MenuGenerator } from "./menu-generator"
import NavProfile from "./nav-profile"
import { getSidebarSections } from "./sidebar-menu-config"

export default function AdminSidebar({
  ...props
}: React.ComponentProps<typeof Sidebar>) {
  const t = useTranslations()
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""
  const enabledModules = useEnabledModules(tenantId)

  const tFn = (key: string) => t(key as Parameters<typeof t>[0])

  const sections = getSidebarSections(tFn).filter(
    (section) => !section.module || enabledModules.includes(section.module),
  )

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
        {sections.map((section) => (
          <MenuGenerator
            key={section.label}
            items={section.items}
            groupLabel={section.label}
          />
        ))}
      </SidebarContent>
      <SidebarFooter>
        <NavProfile />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
