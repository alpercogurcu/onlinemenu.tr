"use client"

import { useParams } from "next/navigation"

import { ModifierGroupEditor } from "@/components/catalog/modifier-group-editor"

export default function ModifierGroupDetailPage() {
  const params = useParams<{ id: string }>()
  return <ModifierGroupEditor groupId={params.id} />
}
