"use client"

import { ChevronRight } from "lucide-react"

import { memo, useEffect, useMemo, useState } from "react"

import Link from "next/link"
import { usePathname } from "next/navigation"

import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarMenuSubSub,
  SidebarMenuSubSubButton,
  SidebarMenuSubSubItem,
} from "@/components/ui/sidebar"
import { type MenuItem, isMenuItemActive } from "@/lib/menu-utils"
import { cn } from "@/lib/utils"

interface MenuGeneratorProps {
  items: MenuItem[]
  groupLabel?: string
  collapsible?: boolean
  defaultOpen?: boolean
}

function MenuGeneratorComponent({
  items,
  groupLabel = "Menu",
  collapsible = false,
  defaultOpen,
}: MenuGeneratorProps) {
  const pathname = usePathname()

  const hasActiveChild = useMemo(
    () => items.some((item) => isMenuItemActive(item, pathname)),
    [items, pathname],
  )

  const [isOpen, setIsOpen] = useState(defaultOpen ?? hasActiveChild)

  useEffect(() => {
    if (hasActiveChild) setIsOpen(true)
  }, [hasActiveChild])

  if (items.length === 0) return null

  const menuContent = (
    <SidebarMenu>
      {items.map((item) => (
        <RecursiveMenuItem
          key={item.title}
          item={item}
          pathname={pathname}
          level={1}
        />
      ))}
    </SidebarMenu>
  )

  if (!collapsible) {
    return (
      <SidebarGroup>
        <SidebarGroupLabel>{groupLabel}</SidebarGroupLabel>
        {menuContent}
      </SidebarGroup>
    )
  }

  return (
    <Collapsible
      open={isOpen}
      onOpenChange={setIsOpen}
      className="group/collapsible-group"
    >
      <SidebarGroup>
        <CollapsibleTrigger asChild>
          <SidebarGroupLabel className="cursor-pointer select-none hover:bg-sidebar-accent rounded-md transition-colors">
            {groupLabel}
            <ChevronRight className="ml-auto h-4 w-4 transition-transform duration-200 group-data-[state=open]/collapsible-group:rotate-90" />
          </SidebarGroupLabel>
        </CollapsibleTrigger>
        <CollapsibleContent>{menuContent}</CollapsibleContent>
      </SidebarGroup>
    </Collapsible>
  )
}

interface RecursiveMenuItemProps {
  item: MenuItem
  pathname: string
  level: 1 | 2 | 3
}

const RecursiveMenuItem = memo(
  ({ item, pathname, level }: RecursiveMenuItemProps) => {
    const hasChildren = item.items && item.items.length > 0
    const isActive = isMenuItemActive(item, pathname)
    const [isOpen, setIsOpen] = useState(isActive)

    useEffect(() => {
      if (isActive) {
        setIsOpen(true)
      }
    }, [isActive])

    const MenuItemComponent =
      level === 1
        ? SidebarMenuItem
        : level === 2
          ? SidebarMenuSubItem
          : SidebarMenuSubSubItem

    const MenuButtonComponent =
      level === 1
        ? SidebarMenuButton
        : level === 2
          ? SidebarMenuSubButton
          : SidebarMenuSubSubButton

    const SubMenuComponent =
      level === 1 ? SidebarMenuSub : level === 2 ? SidebarMenuSubSub : null

    if (!hasChildren) {
      return (
        <MenuItemComponent>
          <MenuButtonComponent asChild>
            <Link href={item.url}>
              {level === 1 && item.icon && <item.icon />}
              <span
                className={cn(
                  pathname === item.url && "font-bold",
                  level === 3 && "text-xs",
                )}
              >
                {item.title}
              </span>
            </Link>
          </MenuButtonComponent>
        </MenuItemComponent>
      )
    }

    return (
      <Collapsible
        asChild
        open={isOpen}
        onOpenChange={setIsOpen}
        className="group/collapsible"
      >
        <MenuItemComponent>
          <CollapsibleTrigger asChild>
            {item.url && item.url !== "#" ? (
              <Link href={item.url}>
                <MenuButtonComponent>
                  {level === 1 && item.icon && <item.icon />}
                  <span
                    className={cn(
                      pathname === item.url && "font-bold",
                      level === 3 && "text-xs",
                    )}
                  >
                    {item.title}
                  </span>
                  <ChevronRight className="ml-auto transition-transform duration-200 group-data-[state=open]/collapsible:rotate-90" />
                </MenuButtonComponent>
              </Link>
            ) : (
              <MenuButtonComponent>
                {level === 1 && item.icon && <item.icon />}
                <span className={cn(level === 3 && "text-xs")}>
                  {item.title}
                </span>
                <ChevronRight className="ml-auto transition-transform duration-200 group-data-[state=open]/collapsible:rotate-90" />
              </MenuButtonComponent>
            )}
          </CollapsibleTrigger>
          <CollapsibleContent>
            {SubMenuComponent && (
              <SubMenuComponent>
                {item.items?.map((child) => (
                  <RecursiveMenuItem
                    key={child.title}
                    item={child}
                    pathname={pathname}
                    level={(level === 1 ? 2 : 3) as 1 | 2 | 3}
                  />
                ))}
              </SubMenuComponent>
            )}
          </CollapsibleContent>
        </MenuItemComponent>
      </Collapsible>
    )
  },
)

RecursiveMenuItem.displayName = "RecursiveMenuItem"

export const MenuGenerator = memo(MenuGeneratorComponent)
