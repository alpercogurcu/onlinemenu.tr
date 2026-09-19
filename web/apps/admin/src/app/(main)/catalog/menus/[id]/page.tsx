"use client"

import { useParams } from "next/navigation"

import { MenuItemsEditor } from "@/components/catalog/menu-items-editor"

export default function MenuDetailPage() {
  const params = useParams<{ id: string }>()
  return <MenuItemsEditor menuId={params.id} />
}
