"use client"

import { useParams } from "next/navigation"

import { ProductEditor } from "@/components/catalog/product-editor"

export default function ProductDetailPage() {
  const params = useParams<{ id: string }>()
  return <ProductEditor productId={params.id} />
}
