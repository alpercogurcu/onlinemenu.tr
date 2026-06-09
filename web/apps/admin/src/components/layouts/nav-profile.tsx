"use client"

import { ChevronsUpDown, LogOutIcon, Moon, Sun, User } from "lucide-react"
import { useTheme } from "next-themes"

import { memo, useMemo, useState } from "react"

import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { SidebarMenuButton } from "@/components/ui/sidebar"
import { useAuthStore } from "@/store/auth-store"

function NavProfileComponent() {
  const { user, logout } = useAuthStore()
  const { theme, setTheme } = useTheme()
  const [, setProfileOpen] = useState(false)

  const nameLetter = useMemo(() => {
    if (!user?.name) return "A"
    return user.name
      .split(" ")
      .slice(0, 2)
      .map((n) => String(n[0]).toUpperCase())
      .join("")
  }, [user?.name])

  const toggleTheme = () => {
    setTheme(theme === "dark" ? "light" : "dark")
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <SidebarMenuButton
          size="lg"
          className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground focus-visible:ring-1"
        >
          <div className="grid flex-1 text-right text-sm leading-tight">
            <span className="truncate font-medium">
              {user?.name ?? "Admin"}
            </span>
            <span className="truncate text-xs text-muted-foreground">
              {user?.email ?? "admin@onlinemenu.tr"}
            </span>
          </div>
          <Avatar>
            <AvatarFallback>{nameLetter}</AvatarFallback>
          </Avatar>
          <ChevronsUpDown className="ml-auto size-4" />
        </SidebarMenuButton>
      </DropdownMenuTrigger>
      <DropdownMenuContent className="w-56">
        <DropdownMenuGroup>
          <DropdownMenuItem onClick={() => setProfileOpen(true)}>
            <User className="size-4" />
            <span>Profil</span>
          </DropdownMenuItem>
          <DropdownMenuItem onClick={toggleTheme}>
            {theme === "dark" ? (
              <Sun className="size-4" />
            ) : (
              <Moon className="size-4" />
            )}
            <span>{theme === "dark" ? "Aydınlık Tema" : "Koyu Tema"}</span>
          </DropdownMenuItem>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={logout}>
          <LogOutIcon className="text-current" />
          <span>Çıkış Yap</span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

const NavProfile = memo(NavProfileComponent)

export default NavProfile
