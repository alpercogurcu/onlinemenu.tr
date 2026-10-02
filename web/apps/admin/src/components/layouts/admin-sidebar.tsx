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
import { useCan } from "@/hooks/use-can"
import { usePosBranchSettings } from "@/hooks/use-pos-branch-settings"
import { useSelectedBranch } from "@/hooks/use-selected-branch"
import { useEnabledModules } from "@/lib/modules"
import { canAccessRoute, currentFloorPlanRoute } from "@/lib/route-permissions"
import { useAuthStore } from "@/store/auth-store"

import { MenuGenerator } from "./menu-generator"
import NavProfile from "./nav-profile"
import { getSidebarSections } from "./sidebar-menu-config"

const KITCHEN_URL = "/pos/kitchen"

export default function AdminSidebar({
  ...props
}: React.ComponentProps<typeof Sidebar>) {
  const t = useTranslations()
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""
  // Subscribed so the menu re-derives when the session (CTX token) changes.
  useAuthStore((s) => s.user)
  const enabledModules = useEnabledModules(tenantId)

  const { branchId } = useSelectedBranch()
  // GET /pos/branch-settings needs pos.check.read; the kitchen role lacks it,
  // so the fetch is skipped for it instead of producing a 403 on every page.
  const canReadSettings = useCan("pos.check.read")
  const { data: posSettings } = usePosBranchSettings(branchId, { enabled: canReadSettings })
  // Fail-open: while loading, on error, without the permission or with no
  // branch resolved, `data` is undefined and the kitchen entry stays. The
  // route itself is never blocked — only the menu link is hidden.
  const hideKitchen = posSettings?.order_flow === "simple"

  const tFn = (key: string) => t(key as Parameters<typeof t>[0])

  const sections = getSidebarSections(tFn, canAccessRoute, currentFloorPlanRoute()).filter(
    (section) => !section.module || enabledModules.includes(section.module),
  ).map((section) =>
    hideKitchen
      ? { ...section, items: section.items.filter((item) => item.url !== KITCHEN_URL) }
      : section,
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
