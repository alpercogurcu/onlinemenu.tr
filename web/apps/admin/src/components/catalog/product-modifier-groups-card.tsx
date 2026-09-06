"use client"

import { GripVertical, X } from "lucide-react"
import { useTranslations } from "next-intl"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useState } from "react"
import { toast } from "sonner"

import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  useAssignModifierGroup,
  useCreateModifierGroup,
  useModifierGroups,
  useModifiers,
  useRemoveModifierGroup,
} from "@/hooks/use-catalog"
import type { ModifierGroup } from "@/types"

interface GroupRowProps {
  group: ModifierGroup
  onRemove: (groupId: string, groupName: string) => void
}

// Its own component (rather than a map callback) so useModifiers(group.id) —
// one query per assigned group — stays a top-level hook call per the rules
// of hooks, instead of being invoked inside a loop.
function GroupRow({ group, onRemove }: GroupRowProps) {
  const t = useTranslations("catalog.product.groups")
  const { data: modifiers } = useModifiers(group.id)
  const options = modifiers ?? []

  const names = options.slice(0, 3).map((m) => m.name)
  const extra = options.length - names.length
  const optionsPreview = names.length > 0 ? `${names.join(", ")}${extra > 0 ? ` +${extra}` : ""}` : ""

  const selectionLabel = group.selection_type === "single" ? t("single") : t("multiple")
  const hint = optionsPreview
    ? `${selectionLabel} · ${t("optionsCount", { count: options.length })}: ${optionsPreview}`
    : selectionLabel

  return (
    <div className="flex items-start gap-2 rounded-md border p-3">
      <GripVertical className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium">{group.name}</span>
          <Badge
            variant="outline"
            className={
              group.is_required
                ? "border-orange-200 bg-orange-100 text-orange-700"
                : "border-gray-200 bg-gray-100 text-gray-600"
            }
          >
            {group.is_required ? t("required") : t("optional")}
          </Badge>
        </div>
        <p className="text-xs text-muted-foreground">{hint}</p>
        <Link href={`/catalog/modifiers/${group.id}`} className="text-xs text-primary hover:underline">
          {t("edit")}
        </Link>
      </div>
      <button
        type="button"
        onClick={() => onRemove(group.id, group.name)}
        aria-label={`${group.name} — ${t("remove")}`}
        className="text-muted-foreground hover:text-destructive"
      >
        <X className="size-4" />
      </button>
    </div>
  )
}

interface ProductModifierGroupsCardProps {
  productId: string
  assignedGroupIds: string[]
}

export function ProductModifierGroupsCard({ productId, assignedGroupIds }: ProductModifierGroupsCardProps) {
  const t = useTranslations("catalog.product.groups")
  const tGroups = useTranslations("catalog.groups")
  const router = useRouter()

  const { data: allGroups } = useModifierGroups()
  const groups = allGroups ?? []
  const assignedGroups = groups.filter((g) => assignedGroupIds.includes(g.id))
  const unassignedGroups = groups.filter((g) => !assignedGroupIds.includes(g.id))

  const assignGroup = useAssignModifierGroup()
  const removeGroup = useRemoveModifierGroup()
  const createGroup = useCreateModifierGroup()

  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState("")

  const filtered = unassignedGroups.filter((g) =>
    g.name.toLocaleLowerCase("tr").includes(search.trim().toLocaleLowerCase("tr")),
  )
  const trimmedSearch = search.trim()
  const exactMatch = unassignedGroups.some(
    (g) => g.name.trim().toLocaleLowerCase("tr") === trimmedSearch.toLocaleLowerCase("tr"),
  )

  async function handleAssignExisting(groupId: string) {
    try {
      await assignGroup.mutateAsync({ productId, groupId })
      setOpen(false)
      setSearch("")
    } catch {
      toast.error(tGroups("toast.error"))
    }
  }

  async function handleCreateAndAssign(name: string) {
    try {
      const created = await createGroup.mutateAsync({
        name,
        selection_type: "single",
        min_selections: 0,
        max_selections: 1,
        is_required: false,
      })
      const newGroupId = created.data.id
      await assignGroup.mutateAsync({ productId, groupId: newGroupId })
      setOpen(false)
      setSearch("")
      toast.success(tGroups("toast.created"), {
        action: { label: t("edit"), onClick: () => router.push(`/catalog/modifiers/${newGroupId}`) },
      })
    } catch {
      toast.error(tGroups("toast.error"))
    }
  }

  // Removing a group from a product now surfaces an undoable toast: the row
  // disappearing from the list is still the primary feedback, but a misclick
  // ("kaldır" instead of "düzenle") no longer means redoing the whole
  // search-or-create flow to fix it. The re-assign on undo omits sort_order
  // the same way the original assign flow does (see handleAssignExisting) —
  // the API only ever tracks the row's current position, so "same
  // sort_order" here means "assigned the same way it originally was".
  async function handleRemove(groupId: string, groupName: string) {
    try {
      await removeGroup.mutateAsync({ productId, groupId })
      toast(t("removed", { name: groupName }), {
        action: {
          label: t("undo"),
          onClick: () => {
            assignGroup.mutateAsync({ productId, groupId }).catch(() => toast.error(tGroups("toast.error")))
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
        {assignedGroups.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("empty")}</p>
        ) : (
          assignedGroups.map((group) => <GroupRow key={group.id} group={group} onRemove={handleRemove} />)
        )}

        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <button
              type="button"
              className="w-full rounded-md border border-dashed p-2 text-sm text-muted-foreground hover:border-foreground hover:text-foreground"
            >
              {t("addPlaceholder")}
            </button>
          </PopoverTrigger>
          <PopoverContent className="w-72 p-0" align="start">
            <Command shouldFilter={false}>
              <CommandInput value={search} onValueChange={setSearch} placeholder={t("addPlaceholder")} />
              <CommandList>
                {filtered.length === 0 && trimmedSearch === "" ? <CommandEmpty>{t("empty")}</CommandEmpty> : null}
                <CommandGroup>
                  {filtered.map((group) => (
                    <CommandItem key={group.id} onSelect={() => void handleAssignExisting(group.id)}>
                      {group.name}
                    </CommandItem>
                  ))}
                  {trimmedSearch !== "" && !exactMatch ? (
                    <CommandItem onSelect={() => void handleCreateAndAssign(trimmedSearch)}>
                      {t("createNew", { name: trimmedSearch })}
                    </CommandItem>
                  ) : null}
                </CommandGroup>
              </CommandList>
            </Command>
          </PopoverContent>
        </Popover>
      </CardContent>
    </Card>
  )
}
