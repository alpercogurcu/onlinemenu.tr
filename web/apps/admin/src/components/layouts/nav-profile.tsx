"use client"

import { ChevronsUpDown, LogOutIcon, Moon, Sun, User } from "lucide-react"
import { useTheme } from "next-themes"
import { useRouter } from "next/navigation"

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
import { Skeleton } from "@/components/ui/skeleton"
import { SidebarMenuButton } from "@/components/ui/sidebar"
import { useMe } from "@/hooks/use-identity"
import { useAuthStore } from "@/store/auth-store"

function NavProfileComponent() {
  const { logout } = useAuthStore()
  const { theme, setTheme } = useTheme()
  const [, setProfileOpen] = useState(false)
  const router = useRouter()

  const { data: me, isLoading } = useMe()

  const nameLetter = useMemo(() => {
    if (!me?.full_name) return "A"
    return me.full_name
      .split(" ")
      .slice(0, 2)
      .map((n) => String(n[0]).toUpperCase())
      .join("")
  }, [me?.full_name])

  const toggleTheme = () => {
    setTheme(theme === "dark" ? "light" : "dark")
  }

  const handleLogout = () => {
    logout()
    router.push("/login")
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <SidebarMenuButton
          size="lg"
          className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground focus-visible:ring-1"
        >
          {isLoading ? (
            <div className="grid flex-1 gap-1 text-right">
              <Skeleton className="h-3 w-24 ml-auto" />
              <Skeleton className="h-2.5 w-32 ml-auto" />
            </div>
          ) : (
            <div className="grid flex-1 text-right text-sm leading-tight">
              <span className="truncate font-medium">
                {me?.full_name ?? "Admin"}
              </span>
              <span className="truncate text-xs text-muted-foreground">
                {me?.email ?? "admin@onlinemenu.tr"}
              </span>
            </div>
          )}
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
        <DropdownMenuItem onClick={handleLogout}>
          <LogOutIcon className="text-current" />
          <span>Çıkış Yap</span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

const NavProfile = memo(NavProfileComponent)

export default NavProfile
