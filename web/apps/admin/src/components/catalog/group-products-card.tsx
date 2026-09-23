"use client"

import { Plus, X } from "lucide-react"
import { useTranslations } from "next-intl"
import Link from "next/link"
import { useState } from "react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { useCan } from "@/hooks/use-can"
import {
  useAssignModifierGroup,
  useGroupProductIds,
  useProducts,
  useRemoveModifierGroup,
} from "@/hooks/use-catalog"

/**
 * "Bu grubu kullanan ürünler" — and, for a role holding
 * catalog.modifier_group.assign, the place to attach the group to products.
 * The group page is where a manager who has just built a group looks for
 * "which burgers get this"; the product page's own picker stays as the other
 * way in. Both call the same assign/remove endpoints and invalidate both
 * sides of the relation (hooks/use-catalog.ts).
 */
export function GroupProductsCard({ groupId }: { groupId: string }) {
  const t = useTranslations("catalog.group.usedBy")
  const tGroups = useTranslations("catalog.groups")
  const canAssign = useCan("catalog.modifier_group.assign")

  const { data: productIdsData } = useGroupProductIds(groupId)
  const { data: productsData } = useProducts()
  const assign = useAssignModifierGroup()
  const remove = useRemoveModifierGroup()

  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState("")

  const productIds = productIdsData ?? []
  const products = productsData ?? []
  const nameOf = (id: string) => products.find((p) => p.id === id)?.name ?? id.slice(0, 8)
  const query = search.trim().toLocaleLowerCase("tr")
  const candidates = products
    .filter((p) => !productIds.includes(p.id))
    .filter((p) => p.name.toLocaleLowerCase("tr").includes(query))
    .sort((a, b) => a.name.localeCompare(b.name, "tr"))

  async function handleAssign(productId: string) {
    try {
      await assign.mutateAsync({ productId, groupId })
      toast.success(t("added", { product: nameOf(productId) }))
      setOpen(false)
      setSearch("")
    } catch {
      toast.error(tGroups("toast.error"))
    }
  }

  async function handleRemove(productId: string) {
    try {
      await remove.mutateAsync({ productId, groupId })
      toast(t("removed", { product: nameOf(productId) }), {
        action: {
          label: t("undo"),
          onClick: () => {
            assign.mutateAsync({ productId, groupId }).catch(() => toast.error(tGroups("toast.error")))
          },
        },
      })
    } catch {
      toast.error(tGroups("toast.error"))
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("title")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {productIds.length === 0 ? (
          <p className="text-sm text-muted-foreground">{canAssign ? t("emptyAssign") : t("empty")}</p>
        ) : (
          <>
            <ul className="space-y-1">
              {productIds.map((id) => (
                <li key={id} className="flex min-h-11 items-center justify-between gap-2 rounded-md border px-3">
                  <Link href={`/catalog/products/${id}`} className="min-w-0 truncate text-sm text-primary hover:underline">
                    {nameOf(id)}
                  </Link>
                  {canAssign && (
                    <Button
                      variant="ghost"
                      size="icon"
                      className="shrink-0 text-muted-foreground hover:text-destructive"
                      aria-label={t("removeAria", { product: nameOf(id) })}
                      onClick={() => void handleRemove(id)}
                      disabled={remove.isPending}
                    >
                      <X className="size-4" />
                    </Button>
                  )}
                </li>
              ))}
            </ul>
            <p className="text-xs text-muted-foreground">{t("hint", { n: productIds.length })}</p>
          </>
        )}

        {canAssign && (
          <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
              <Button variant="outline" className="h-11 w-full">
                <Plus className="size-4" />
                {t("add")}
              </Button>
            </PopoverTrigger>
            <PopoverContent className="w-72 p-0" align="start">
              <Command shouldFilter={false}>
                <CommandInput value={search} onValueChange={setSearch} placeholder={t("searchPlaceholder")} />
                <CommandList>
                  {candidates.length === 0 && <CommandEmpty>{t("noCandidates")}</CommandEmpty>}
                  <CommandGroup>
                    {candidates.map((p) => (
                      <CommandItem key={p.id} value={p.id} onSelect={() => void handleAssign(p.id)}>
                        {p.name}
                      </CommandItem>
                    ))}
                  </CommandGroup>
                </CommandList>
              </Command>
            </PopoverContent>
          </Popover>
        )}
      </CardContent>
    </Card>
  )
}
