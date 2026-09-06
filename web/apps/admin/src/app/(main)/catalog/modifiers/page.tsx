"use client"

import { useQueries } from "@tanstack/react-query"
import { MoreVertical, Plus, UtensilsCrossed } from "lucide-react"
import { useTranslations } from "next-intl"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useState } from "react"
import { toast } from "sonner"

import { ConfirmDialog } from "@/components/catalog/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useDeleteModifierGroup, useModifierGroups } from "@/hooks/use-catalog"
import api from "@/lib/api"
import type { Modifier, ModifierGroup } from "@/types"

// One request per group, matching the exact query key useModifiers(groupId)
// uses — so the count fetched here and the modifiers fetched on the detail
// page share the same cache entry, same batching approach as
// useProductsModifierGroupIds in use-catalog.ts.
function useModifierCounts(groupIds: string[]): Record<string, number> {
  const results = useQueries({
    queries: groupIds.map((groupId) => ({
      queryKey: ["modifiers", groupId] as const,
      queryFn: async () => {
        const { data } = await api.get<Modifier[]>(
          `/api/v1/catalog/modifier-groups/${groupId}/modifiers`,
        )
        return data ?? []
      },
      enabled: Boolean(groupId),
    })),
  })

  const counts: Record<string, number> = {}
  groupIds.forEach((groupId, i) => {
    counts[groupId] = results[i]?.data?.length ?? 0
  })
  return counts
}

// Same batching approach for useGroupProductIds(groupId)'s query key.
function useGroupProductCounts(groupIds: string[]): Record<string, number> {
  const results = useQueries({
    queries: groupIds.map((groupId) => ({
      queryKey: ["group-products", groupId] as const,
      queryFn: async () => {
        const { data } = await api.get<string[]>(
          `/api/v1/catalog/modifier-groups/${groupId}/products`,
        )
        return data ?? []
      },
      enabled: Boolean(groupId),
    })),
  })

  const counts: Record<string, number> = {}
  groupIds.forEach((groupId, i) => {
    counts[groupId] = results[i]?.data?.length ?? 0
  })
  return counts
}

type RuleT = (key: string, values?: Record<string, string | number>) => string

// "Tek seçim · Zorunlu" for a single-selection group, "Birden fazla · en fazla
// 3" for a capped multi-selection one. The second segment is required/
// optional for "single" (max_selections there is always 1, not worth
// repeating) and the max cap for "multiple" (its required/optional flag is
// edited on the detail page, not summarised here).
function formatRule(group: ModifierGroup, t: RuleT): string {
  const selectionLabel = group.selection_type === "single" ? t("rule.single") : t("rule.multiple")
  const secondSegment =
    group.selection_type === "single"
      ? group.is_required
        ? t("rule.required")
        : t("rule.optional")
      : group.max_selections === null
        ? t("rule.unlimited")
        : t("rule.max", { max: group.max_selections })
  return `${selectionLabel} · ${secondSegment}`
}

export default function ModifiersPage() {
  const t = useTranslations("catalog.groups")
  const tGroup = useTranslations("catalog.group")
  const router = useRouter()

  const { data, isLoading } = useModifierGroups()
  const deleteGroup = useDeleteModifierGroup()

  const [deleteTarget, setDeleteTarget] = useState<ModifierGroup | null>(null)

  const groups = data ?? []
  const groupIds = groups.map((g) => g.id)
  const optionCounts = useModifierCounts(groupIds)
  const productCounts = useGroupProductCounts(groupIds)

  function goToNew() {
    router.push("/catalog/modifiers/new")
  }

  async function handleDelete() {
    if (!deleteTarget) return
    try {
      await deleteGroup.mutateAsync(deleteTarget.id)
      toast.success(t("toast.deleted"))
    } catch {
      toast.error(t("toast.error"))
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("title")}</h1>
          <p className="text-muted-foreground">{t("subtitle")}</p>
        </div>
        <Button onClick={goToNew}>
          <Plus className="size-4" />
          {t("add")}
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t("title")}</CardTitle>
          <CardDescription>{t("subtitle")}</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-12 w-full" />
              ))}
            </div>
          ) : groups.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <UtensilsCrossed className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">{t("empty.title")}</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">{t("empty.body")}</p>
              <Button onClick={goToNew}>
                <Plus className="size-4" />
                {t("empty.cta")}
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("columns.name")}</TableHead>
                  <TableHead>{t("columns.rule")}</TableHead>
                  <TableHead>{t("columns.options")}</TableHead>
                  <TableHead>{t("columns.products")}</TableHead>
                  <TableHead className="w-[48px]" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {groups.map((group) => (
                  <TableRow key={group.id}>
                    <TableCell className="font-medium">
                      <Link href={`/catalog/modifiers/${group.id}`} className="hover:underline">
                        {group.name}
                      </Link>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{formatRule(group, t)}</TableCell>
                    <TableCell>{optionCounts[group.id] ?? 0}</TableCell>
                    <TableCell>{productCounts[group.id] ?? 0}</TableCell>
                    <TableCell>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon" aria-label={`${group.name} için işlemler`}>
                            <MoreVertical className="size-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem
                            onSelect={() => router.push(`/catalog/modifiers/${group.id}`)}
                          >
                            {tGroup("edit")}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            variant="destructive"
                            onSelect={() => setDeleteTarget(group)}
                          >
                            {tGroup("delete")}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
        title={t("deleteConfirm.title")}
        description={t("deleteConfirm.body", {
          count: deleteTarget ? (productCounts[deleteTarget.id] ?? 0) : 0,
        })}
        confirmLabel={t("deleteConfirm.confirm")}
        destructive
        onConfirm={handleDelete}
      />
    </div>
  )
}
