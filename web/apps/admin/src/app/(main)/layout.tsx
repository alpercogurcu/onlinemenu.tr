"use client"

import type { ReactNode } from "react"
import { useEffect, useLayoutEffect, useState } from "react"

import AdminSidebar from "@/components/layouts/admin-sidebar"
import DynamicBreadcrumb from "@/components/layouts/dynamic-breadcrumb"
import NavProfile from "@/components/layouts/nav-profile"
import { Separator } from "@/components/ui/separator"
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { Skeleton } from "@/components/ui/skeleton"

function SidebarSkeleton() {
  return (
    <div className="flex h-full w-[16rem] flex-col gap-2 p-2">
      <div className="flex items-center justify-center py-2">
        <Skeleton className="h-8 w-32" />
      </div>
      {Array.from({ length: 8 }).map((_, i) => (
        <Skeleton key={i} className="h-8 w-full" />
      ))}
    </div>
  )
}

export default function MainLayout({
  children,
}: Readonly<{ children: ReactNode }>) {
  const [isMounted, setIsMounted] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(true)

  useEffect(() => {
    setIsMounted(true)
  }, [])

  useLayoutEffect(() => {
    const savedSidebarState = localStorage.getItem("sidebarOpen")
    if (savedSidebarState !== null) {
      setSidebarOpen(JSON.parse(savedSidebarState))
    }
  }, [])

  if (!isMounted) {
    return (
      <SidebarProvider suppressHydrationWarning defaultOpen={sidebarOpen}>
        <div className="flex w-full">
          <SidebarSkeleton />
          <SidebarInset className="flex-1 flex flex-col min-w-0">
            <header className="flex h-16 shrink-0 items-center gap-2 border-b px-4" />
          </SidebarInset>
        </div>
      </SidebarProvider>
    )
  }

  return (
    <SidebarProvider suppressHydrationWarning defaultOpen={sidebarOpen}>
      <div className="flex w-full">
        <AdminSidebar />
        <SidebarInset className="flex-1 flex flex-col min-w-0">
          <header className="flex h-16 shrink-0 items-center gap-2 border-b px-4">
            <SidebarTrigger className="-ml-1" />
            <Separator orientation="vertical" className="mr-2 h-4" />
            <DynamicBreadcrumb />
            <div className="flex items-center gap-2 ml-auto">
              <NavProfile />
            </div>
          </header>
          <div>
            <div className="p-4">{children}</div>
          </div>
        </SidebarInset>
      </div>
    </SidebarProvider>
  )
}
