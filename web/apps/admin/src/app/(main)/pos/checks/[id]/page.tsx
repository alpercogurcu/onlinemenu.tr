"use client"

import { useParams } from "next/navigation"

import { CheckDetail } from "@/components/pos/check-detail"

export default function CheckDetailPage() {
  const params = useParams<{ id: string }>()
  return <CheckDetail checkId={params.id} />
}
