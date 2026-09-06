"use client"

import { useTranslations } from "next-intl"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useEffect, useState } from "react"
import { toast } from "sonner"

import { ConfirmDialog } from "@/components/catalog/confirm-dialog"
import { ModifierGroupForm } from "@/components/catalog/modifier-group-form"
import { ModifierOptionsEditor } from "@/components/catalog/modifier-options-editor"
import { ModifierPreview } from "@/components/catalog/modifier-preview"
import { useBreadcrumbLabel } from "@/components/layouts/dynamic-breadcrumb"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  useCreateModifierGroup,
  useDeleteModifierGroup,
  useGroupProductIds,
  useModifierGroup,
  useModifiers,
  useProducts,
  useUpdateModifierGroup,
} from "@/hooks/use-catalog"
import type { SelectionType } from "@/types"

interface ModifierGroupEditorProps {
  // null selects "new" mode — no group exists yet, so the options card and
  // delete button stay hidden until the first Save creates one.
  groupId: string | null
}

export function ModifierGroupEditor({ groupId }: ModifierGroupEditorProps) {
  const t = useTranslations("catalog.group")
  const tGroups = useTranslations("catalog.groups")
  const router = useRouter()
  const isNew = groupId === null

  const { data: group, isLoading: groupLoading } = useModifierGroup(groupId ?? "")
  // Lets the shared breadcrumb show the group's own name as the last crumb
  // instead of the raw UUID route param (mirrors ProductEditor). Undefined
  // while loading or on the "new" route, where the breadcrumb already falls
  // back to "Yeni Grup" via NEW_SEGMENT_NAMES.
  useBreadcrumbLabel(group?.name)
  const { data: modifiersData } = useModifiers(groupId ?? "")
  const { data: productIdsData } = useGroupProductIds(groupId ?? "")
  const { data: productsData } = useProducts()

  const createGroup = useCreateModifierGroup()
  const updateGroup = useUpdateModifierGroup()
  const deleteGroup = useDeleteModifierGroup()

  const [name, setName] = useState("")
  const [selectionType, setSelectionType] = useState<SelectionType>("single")
  const [isRequired, setIsRequired] = useState(false)
  // Only meaningful while selectionType is "multiple" — see handleSave, which
  // forces max_selections to 1 whenever selectionType is "single" regardless
  // of whatever this still holds from a previous "multiple" selection.
  const [maxSelections, setMaxSelections] = useState<number | null>(null)
  const [nameError, setNameError] = useState<string | undefined>()
  const [maxError, setMaxError] = useState<string | undefined>()
  const [deleteOpen, setDeleteOpen] = useState(false)

  // Populate the form once the existing group loads. Runs again if a
  // different groupId is loaded into the same mounted editor (shouldn't
  // happen with the current routing, but keeps this correct either way).
  useEffect(() => {
    if (!group) return
    setName(group.name)
    setSelectionType(group.selection_type)
    setIsRequired(group.is_required)
    setMaxSelections(group.max_selections)
  }, [group])

  const modifiers = modifiersData ?? []
  const productIds = productIdsData ?? []
  const products = productsData ?? []

  async function handleSave() {
    const trimmedName = name.trim()
    const minSelections = isRequired ? 1 : 0
    const effectiveMax = selectionType === "single" ? 1 : maxSelections

    const nextNameError = trimmedName ? undefined : t("validation.nameRequired")
    const nextMaxError =
      selectionType === "multiple" && effectiveMax !== null && effectiveMax < minSelections
        ? t("validation.maxLessThanMin")
        : undefined
    setNameError(nextNameError)
    setMaxError(nextMaxError)
    if (nextNameError || nextMaxError) return

    const body = {
      name: trimmedName,
      selection_type: selectionType,
      max_selections: effectiveMax,
      min_selections: minSelections,
      is_required: isRequired,
    }

    try {
      if (isNew) {
        const res = await createGroup.mutateAsync(body)
        toast.success(tGroups("toast.created"))
        router.replace(`/catalog/modifiers/${res.data.id}`)
      } else if (groupId) {
        await updateGroup.mutateAsync({ id: groupId, ...body })
        toast.success(tGroups("toast.updated"))
      }
    } catch {
      toast.error(tGroups("toast.error"))
    }
  }

  async function handleDelete() {
    if (!groupId) return
    try {
      await deleteGroup.mutateAsync(groupId)
      toast.success(tGroups("toast.deleted"))
      router.push("/catalog/modifiers")
    } catch {
      toast.error(tGroups("toast.error"))
    }
  }

  if (!isNew && groupLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-64 w-full" />
      </div>
    )
  }

  const title = name.trim() || (isNew ? t("new") : t("edit"))
  const saving = createGroup.isPending || updateGroup.isPending

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{title}</h1>
          {!isNew ? (
            <p className="text-muted-foreground">
              {t("summary", { options: modifiers.length, products: productIds.length })}
            </p>
          ) : null}
        </div>
        <div className="flex gap-2">
          {!isNew ? (
            <Button
              type="button"
              variant="outline"
              className="text-destructive hover:text-destructive"
              onClick={() => setDeleteOpen(true)}
            >
              {t("delete")}
            </Button>
          ) : null}
          <Button type="button" variant="outline" onClick={() => router.push("/catalog/modifiers")}>
            {t("cancel")}
          </Button>
          <Button type="button" onClick={() => void handleSave()} disabled={saving}>
            {t("save")}
          </Button>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-12">
        <div className="space-y-6 lg:col-span-8">
          <ModifierGroupForm
            name={name}
            onNameChange={setName}
            selectionType={selectionType}
            onSelectionTypeChange={setSelectionType}
            isRequired={isRequired}
            onIsRequiredChange={setIsRequired}
            maxSelections={maxSelections}
            onMaxSelectionsChange={setMaxSelections}
            nameError={nameError}
            maxError={maxError}
          />
          <ModifierOptionsEditor groupId={groupId} />
        </div>

        <div className="space-y-6 lg:col-span-4">
          <ModifierPreview
            name={name}
            selectionType={selectionType}
            isRequired={isRequired}
            modifiers={modifiers}
          />

          <Card>
            <CardHeader>
              <CardTitle>{t("usedBy.title")}</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2">
              {productIds.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t("usedBy.empty")}</p>
              ) : (
                <>
                  <ul className="space-y-1 text-sm">
                    {productIds.map((id) => {
                      const product = products.find((p) => p.id === id)
                      return (
                        <li key={id}>
                          <Link href={`/catalog/products/${id}`} className="text-primary hover:underline">
                            {product?.name ?? id.slice(0, 8)}
                          </Link>
                        </li>
                      )
                    })}
                  </ul>
                  <p className="text-xs text-muted-foreground">
                    {t("usedBy.hint", { n: productIds.length })}
                  </p>
                </>
              )}
            </CardContent>
          </Card>
        </div>
      </div>

      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={tGroups("deleteConfirm.title")}
        description={tGroups("deleteConfirm.body", { count: productIds.length })}
        confirmLabel={tGroups("deleteConfirm.confirm")}
        destructive
        onConfirm={handleDelete}
      />
    </div>
  )
}
