import type { LucideIcon } from "lucide-react"

export interface MenuItem {
  title: string
  url: string
  icon?: LucideIcon
  isActive?: boolean
  items?: MenuItem[]
}

export const isMenuItemActive = (item: MenuItem, pathname: string): boolean => {
  const isDirectMatch =
    pathname === item.url ||
    (pathname.startsWith(item.url + "/") &&
      item.url !== "/" &&
      item.url !== "#")

  if (isDirectMatch) return true

  if (item.items && item.items.length > 0) {
    return item.items.some((child) => isMenuItemActive(child, pathname))
  }

  return false
}
