"use client"

import { ChevronDown, ChevronUp, Loader2, Trash2 } from "lucide-react"
import { useTranslations } from "next-intl"
import { useEffect, useState } from "react"

import { ConfirmDialog } from "@/components/catalog/confirm-dialog"
import { MoneyInput } from "@/components/catalog/money-input"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  useCreateModifier,
  useDeleteModifier,
  useModifiers,
  useUpdateModifier,
} from "@/hooks/use-catalog"
import type { Modifier } from "@/types"

type UpdatableFields = Partial<Pick<Modifier, "name" | "price_delta" | "is_active" | "sort_order">>
type FullModifierBody = Pick<Modifier, "name" | "price_delta" | "is_active" | "sort_order">

// Backend PUT REPLACES the whole modifier row from the request body. A
// partial body (reorder sending only {sort_order}, a field edit sending only
// the changed field) blanks out the rest of the row server-side — this is
// how edited/reordered options used to end up with name:"" and
// is_active:false. Every PUT must carry all four fields: merge them from the
// current row (latest server data, plus any unsaved local edits for that row
// folded in by the caller) with the just-changed field layered on top.
function buildFullBody(current: Modifier, patch: UpdatableFields): FullModifierBody {
  return {
    name: patch.name ?? current.name,
    price_delta: patch.price_delta ?? current.price_delta,
    is_active: patch.is_active ?? current.is_active,
    sort_order: patch.sort_order ?? current.sort_order,
  }
}

interface ModifierOptionsEditorProps {
  // null while the group itself has not been created yet (the "new" route) —
  // rows can't exist without a group_id to attach to, so the card shows a
  // "save the group first" notice instead of the table.
  groupId: string | null
}

export function ModifierOptionsEditor({ groupId }: ModifierOptionsEditorProps) {
  const t = useTranslations("catalog.group")
  const { data } = useModifiers(groupId ?? "")
  const createModifier = useCreateModifier()
  const updateModifier = useUpdateModifier()
  const deleteModifier = useDeleteModifier()

  const [newName, setNewName] = useState("")
  const [deleteTarget, setDeleteTarget] = useState<Modifier | null>(null)
  // Input (components/ui/input.tsx) is a plain function component, not
  // forwardRef — so refocusing after create goes through the DOM id it
  // spreads onto the native <input> rather than a React ref.
  const ADD_ROW_ID = "modifier-add-row"

  const modifiers = [...(data ?? [])].sort((a, b) => a.sort_order - b.sort_order)

  // useUpdateModifier is one shared mutation instance for every row — while it
  // is in flight, `.variables` still holds the last call's arguments, so the
  // id on it tells us exactly which row's PUT is outstanding without any
  // per-row mutation bookkeeping of our own.
  const pendingVariables = updateModifier.variables as { id: string } | undefined
  const savingId = updateModifier.isPending ? pendingVariables?.id : undefined

  async function handleCreate(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key !== "Enter") return
    const trimmed = newName.trim()
    if (!trimmed || !groupId) return
    const lastOrder = modifiers.length > 0 ? modifiers[modifiers.length - 1].sort_order : 0
    await createModifier.mutateAsync({
      groupId,
      name: trimmed,
      price_delta: 0,
      is_active: true,
      sort_order: lastOrder + 10,
    })
    setNewName("")
    document.getElementById(ADD_ROW_ID)?.focus()
  }

  function handleUpdate(current: Modifier, patch: UpdatableFields) {
    if (!groupId) return
    void updateModifier.mutateAsync({ groupId, id: current.id, ...buildFullBody(current, patch) })
  }

  function handleMove(index: number, direction: -1 | 1) {
    if (!groupId) return
    const current = modifiers[index]
    const other = modifiers[index + direction]
    if (!current || !other) return
    void updateModifier.mutateAsync({
      groupId,
      id: current.id,
      ...buildFullBody(current, { sort_order: other.sort_order }),
    })
    void updateModifier.mutateAsync({
      groupId,
      id: other.id,
      ...buildFullBody(other, { sort_order: current.sort_order }),
    })
  }

  async function handleDelete() {
    if (!groupId || !deleteTarget) return
    await deleteModifier.mutateAsync({ groupId, id: deleteTarget.id })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("sections.options")}</CardTitle>
        <CardDescription>{t("options.hint")}</CardDescription>
      </CardHeader>
      <CardContent>
        {groupId === null ? (
          // No dedicated i18n key covers this notice — see Task 5 report's
          // open points, flagged for the team rather than added to tr.json.
          <p className="text-sm text-muted-foreground">{"Önce grubu kaydedin."}</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-[72px]" />
                <TableHead>{t("options.name")}</TableHead>
                <TableHead>{t("options.priceDelta")}</TableHead>
                <TableHead>{t("options.active")}</TableHead>
                <TableHead className="w-[64px]" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {modifiers.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={5} className="text-center text-sm text-muted-foreground">
                    {t("options.empty")}
                  </TableCell>
                </TableRow>
              ) : null}
              {modifiers.map((modifier, index) => (
                <OptionRow
                  key={modifier.id}
                  modifier={modifier}
                  isFirst={index === 0}
                  isLast={index === modifiers.length - 1}
                  saving={savingId === modifier.id}
                  onMoveUp={() => handleMove(index, -1)}
                  onMoveDown={() => handleMove(index, 1)}
                  onUpdate={handleUpdate}
                  onRequestDelete={() => setDeleteTarget(modifier)}
                />
              ))}
              <TableRow>
                <TableCell />
                <TableCell colSpan={4}>
                  <Input
                    id={ADD_ROW_ID}
                    value={newName}
                    onChange={(e) => setNewName(e.target.value)}
                    onKeyDown={(e) => void handleCreate(e)}
                    placeholder={t("options.addPlaceholder")}
                    aria-label={t("options.addRow")}
                    className="border-dashed"
                  />
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
        )}
      </CardContent>

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
        title={t("options.deleteConfirm")}
        confirmLabel={t("options.delete")}
        destructive
        onConfirm={handleDelete}
      />
    </Card>
  )
}

interface OptionRowProps {
  modifier: Modifier
  isFirst: boolean
  isLast: boolean
  saving: boolean
  onMoveUp: () => void
  onMoveDown: () => void
  onUpdate: (current: Modifier, patch: UpdatableFields) => void
  onRequestDelete: () => void
}

function OptionRow({
  modifier,
  isFirst,
  isLast,
  saving,
  onMoveUp,
  onMoveDown,
  onUpdate,
  onRequestDelete,
}: OptionRowProps) {
  const t = useTranslations("catalog.group.options")
  const tValidation = useTranslations("catalog.group.validation")
  const [name, setName] = useState(modifier.name)
  const [priceKurus, setPriceKurus] = useState<number | null>(modifier.price_delta)
  const [nameError, setNameError] = useState(false)

  // Re-sync local edit state whenever the server row changes underneath us
  // (a sibling row's sort_order swap refetches this row too, a delete removes
  // it, etc.) — without this, editing row A would keep showing row A's old
  // text after an unrelated mutation updated the cache.
  useEffect(() => {
    setName(modifier.name)
    setNameError(false)
  }, [modifier.name])
  useEffect(() => setPriceKurus(modifier.price_delta), [modifier.price_delta])

  // The "current row" as far as this PUT is concerned: the latest server
  // data, with whatever this row's own unsaved name/price edits currently
  // hold folded in — so a PUT triggered by one field (e.g. the active
  // toggle) doesn't clobber an in-progress edit to another field with a
  // stale server value.
  function currentRow(): Modifier {
    return {
      ...modifier,
      name: name.trim() || modifier.name,
      price_delta: priceKurus ?? modifier.price_delta,
    }
  }

  function commitName() {
    const trimmed = name.trim()
    if (!trimmed) {
      // Never send an empty name — restore the last known-good value and
      // surface inline validation instead of firing the PUT.
      setName(modifier.name)
      setNameError(true)
      return
    }
    if (trimmed !== modifier.name) {
      onUpdate(currentRow(), { name: trimmed })
    }
  }

  function commitPrice() {
    if (priceKurus !== null && priceKurus !== modifier.price_delta) {
      onUpdate(currentRow(), { price_delta: priceKurus })
    }
  }

  return (
    <TableRow>
      <TableCell>
        <div className="flex gap-0.5">
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            disabled={isFirst}
            aria-disabled={isFirst}
            aria-label={t("moveUp")}
            onClick={onMoveUp}
          >
            <ChevronUp className="size-3.5" />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            disabled={isLast}
            aria-disabled={isLast}
            aria-label={t("moveDown")}
            onClick={onMoveDown}
          >
            <ChevronDown className="size-3.5" />
          </Button>
        </div>
      </TableCell>
      <TableCell>
        <Label htmlFor={`option-name-${modifier.id}`} className="sr-only">
          {t("name")}
        </Label>
        <Input
          id={`option-name-${modifier.id}`}
          value={name}
          onChange={(e) => {
            setName(e.target.value)
            if (nameError) setNameError(false)
          }}
          onBlur={commitName}
          onKeyDown={(e) => {
            if (e.key === "Enter") e.currentTarget.blur()
          }}
          aria-invalid={nameError}
        />
        {nameError ? (
          <p className="text-sm text-destructive">{tValidation("nameRequired")}</p>
        ) : null}
      </TableCell>
      <TableCell>
        <Label htmlFor={`option-price-${modifier.id}`} className="sr-only">
          {t("priceDelta")}
        </Label>
        {/* MoneyInput doesn't forward onBlur/onKeyDown to its own callers, so
            the wrapping div catches both: React implements onBlur via
            capture-phase focus/blur listening, so it fires for the nested
            input's blur even though native "blur" itself never bubbles. */}
        <div onBlur={commitPrice} onKeyDown={(e) => e.key === "Enter" && commitPrice()}>
          <MoneyInput
            id={`option-price-${modifier.id}`}
            valueKurus={priceKurus}
            onChangeKurus={setPriceKurus}
            allowNegative
          />
        </div>
      </TableCell>
      <TableCell>
        <Switch
          checked={modifier.is_active}
          onCheckedChange={(checked) => onUpdate(currentRow(), { is_active: checked })}
          aria-label={t("active")}
        />
      </TableCell>
      <TableCell>
        <div className="flex items-center justify-end gap-1">
          {saving ? <Loader2 className="size-3.5 animate-spin text-muted-foreground" /> : null}
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={`"${modifier.name}" ${t("delete").toLowerCase()}`}
            onClick={onRequestDelete}
          >
            <Trash2 className="size-3.5 text-destructive" />
          </Button>
        </div>
      </TableCell>
    </TableRow>
  )
}
