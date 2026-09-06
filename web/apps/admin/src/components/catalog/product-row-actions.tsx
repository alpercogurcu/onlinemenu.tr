"use client"

import { MoreVertical } from "lucide-react"
import { useTranslations } from "next-intl"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import type { Product } from "@/types"

interface ProductRowActionsProps {
  product: Product
  onEdit: (product: Product) => void
  onDuplicate: (product: Product) => void
  onToggleActive: (product: Product) => void
  onDeleteRequest: (product: Product) => void
}

// Row kebab menu (Düzenle, Kopyala, Satıştan kaldır/Satışa al, Sil). Lives in
// its own component (rather than inline in the table cell) mainly so its
// trigger/content clicks can stopPropagation independently of the row's own
// onClick navigation without cluttering the page component.
export function ProductRowActions({
  product,
  onEdit,
  onDuplicate,
  onToggleActive,
  onDeleteRequest,
}: ProductRowActionsProps) {
  const t = useTranslations("catalog.products.actions")

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={`${product.name} için işlemler`}
          onClick={(e) => e.stopPropagation()}
        >
          <MoreVertical className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
        <DropdownMenuItem onSelect={() => onEdit(product)}>{t("edit")}</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => onDuplicate(product)}>{t("duplicate")}</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => onToggleActive(product)}>
          {product.is_active ? t("deactivate") : t("activate")}
        </DropdownMenuItem>
        <DropdownMenuItem variant="destructive" onSelect={() => onDeleteRequest(product)}>
          {t("delete")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
