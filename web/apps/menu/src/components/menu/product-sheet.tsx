"use client"

import { MinusIcon, PlusIcon } from "lucide-react"
import { useTranslations } from "next-intl"
import { useMemo, useState } from "react"

import {
  Button,
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  cn,
} from "@onlinemenu/ui-kit"

import { MAX_LINE_QUANTITY, useCartStore } from "@/lib/cart-store"
import { formatKurus, formatKurusDelta } from "@/lib/money"
import {
  defaultSelection,
  maxSelections,
  selectedModifiers,
  selectionPriceDelta,
  toggleModifier,
  unsatisfiedRequiredGroups,
  type ModifierSelection,
} from "@/lib/modifiers"
import type { MenuModifierGroup, MenuProduct } from "@/types/storefront"

const MAX_NOTE_LENGTH = 500

export function ProductSheet({
  product,
  onClose,
}: {
  product: MenuProduct | null
  onClose: () => void
}) {
  return (
    <Sheet open={product !== null} onOpenChange={(open) => (open ? null : onClose())}>
      {product === null ? null : (
        // Keying on the product id remounts the body for each product, which
        // is what resets the selection and note — cheaper and harder to get
        // wrong than clearing state in an effect.
        <ProductSheetBody key={product.id} product={product} onClose={onClose} />
      )}
    </Sheet>
  )
}

function ProductSheetBody({
  product,
  onClose,
}: {
  product: MenuProduct
  onClose: () => void
}) {
  const t = useTranslations("product")
  const tCommon = useTranslations("common")
  const addLine = useCartStore((s) => s.addLine)

  const [selection, setSelection] = useState<ModifierSelection>(() =>
    defaultSelection(product.modifier_groups),
  )
  const [quantity, setQuantity] = useState(1)
  const [note, setNote] = useState("")
  const [showValidation, setShowValidation] = useState(false)

  const missingGroups = useMemo(
    () => unsatisfiedRequiredGroups(product.modifier_groups, selection),
    [product.modifier_groups, selection],
  )

  const unitPrice =
    product.price_amount + selectionPriceDelta(product.modifier_groups, selection)
  const totalPrice = unitPrice * quantity

  function handleAdd() {
    if (missingGroups.length > 0) {
      setShowValidation(true)
      return
    }
    addLine(product, selectedModifiers(product.modifier_groups, selection), note, quantity)
    onClose()
  }

  return (
    <SheetContent
      side="bottom"
      closeLabel={tCommon("close")}
      className="max-h-[90dvh] rounded-t-2xl"
    >
      <SheetHeader className="pr-12 text-left">
        <SheetTitle className="text-lg">{product.name}</SheetTitle>
        {product.description === "" ? null : (
          <SheetDescription>{product.description}</SheetDescription>
        )}
      </SheetHeader>

      <div className="flex-1 overflow-y-auto px-4">
        {product.modifier_groups.map((group) => (
          <ModifierGroupSection
            key={group.id}
            group={group}
            selected={selection[group.id] ?? []}
            onToggle={(modifierId) =>
              setSelection((current) => toggleModifier(current, group, modifierId))
            }
          />
        ))}

        <div className="mt-6">
          <label htmlFor="line-note" className="text-sm font-medium">
            {t("note")}
          </label>
          <input
            id="line-note"
            type="text"
            inputMode="text"
            maxLength={MAX_NOTE_LENGTH}
            value={note}
            onChange={(event) => setNote(event.target.value)}
            placeholder={t("notePlaceholder")}
            className="border-input bg-background focus-visible:ring-ring/50 mt-2 h-11 w-full rounded-lg border px-3 text-base outline-none focus-visible:ring-[3px]"
          />
        </div>

        {showValidation && missingGroups.length > 0 ? (
          <p className="text-destructive mt-4 text-sm" role="alert">
            {t("requiredMissing", {
              groups: missingGroups.map((group) => group.name).join(", "),
            })}
          </p>
        ) : null}
      </div>

      <SheetFooter className="gap-3 border-t">
        <div className="flex items-center gap-3">
          <QuantityStepper quantity={quantity} onChange={setQuantity} />
          <Button size="touch-lg" className="flex-1" onClick={handleAdd}>
            {t("addWithPrice", { price: formatKurus(totalPrice) })}
          </Button>
        </div>
      </SheetFooter>
    </SheetContent>
  )
}

function QuantityStepper({
  quantity,
  onChange,
}: {
  quantity: number
  onChange: (quantity: number) => void
}) {
  const t = useTranslations("common")
  return (
    <div className="flex items-center gap-1 rounded-xl border p-1">
      <Button
        size="icon-touch"
        variant="ghost"
        aria-label={t("decrease")}
        disabled={quantity <= 1}
        onClick={() => onChange(Math.max(1, quantity - 1))}
      >
        <MinusIcon />
      </Button>
      <span className="w-8 text-center text-base font-semibold tabular-nums" aria-live="polite">
        {quantity}
      </span>
      <Button
        size="icon-touch"
        variant="ghost"
        aria-label={t("increase")}
        disabled={quantity >= MAX_LINE_QUANTITY}
        onClick={() => onChange(Math.min(MAX_LINE_QUANTITY, quantity + 1))}
      >
        <PlusIcon />
      </Button>
    </div>
  )
}

function ModifierGroupSection({
  group,
  selected,
  onToggle,
}: {
  group: MenuModifierGroup
  selected: string[]
  onToggle: (modifierId: string) => void
}) {
  const t = useTranslations("product")
  const isSingle = group.selection_type === "single"
  const limit = maxSelections(group)
  const atLimit = !isSingle && selected.length >= limit

  return (
    <section className="mt-6 first:mt-4">
      <h3 className="text-sm font-semibold">{group.name}</h3>
      <p className="text-muted-foreground text-xs">{groupHint(group, t)}</p>

      <div
        role={isSingle ? "radiogroup" : "group"}
        aria-label={group.name}
        className="mt-2 flex flex-col gap-2"
      >
        {group.modifiers.map((modifier) => {
          const isChecked = selected.includes(modifier.id)
          const isBlocked = atLimit && !isChecked
          return (
            <button
              key={modifier.id}
              type="button"
              // A "single" group is a radio group even when max_select is 0:
              // the server accepts at most one selection from it regardless
              // (WP2 §2), so offering checkboxes would build a cart it rejects.
              role={isSingle ? "radio" : "checkbox"}
              aria-checked={isChecked}
              disabled={isBlocked}
              onClick={() => onToggle(modifier.id)}
              className={cn(
                "flex min-h-12 items-center gap-3 rounded-xl border px-3 py-2 text-left transition-colors",
                isChecked ? "border-primary bg-primary/5" : "border-border",
                isBlocked && "opacity-40",
              )}
            >
              <span
                className={cn(
                  "flex size-5 shrink-0 items-center justify-center border-2",
                  isSingle ? "rounded-full" : "rounded-md",
                  isChecked ? "border-primary bg-primary" : "border-muted-foreground/40",
                )}
                aria-hidden="true"
              >
                {isChecked ? <span className="bg-background size-2 rounded-full" /> : null}
              </span>
              <span className="flex-1 text-sm">{modifier.name}</span>
              {modifier.price_delta === 0 ? null : (
                <span className="text-muted-foreground text-sm tabular-nums">
                  {formatKurusDelta(modifier.price_delta)}
                </span>
              )}
            </button>
          )
        })}
      </div>
    </section>
  )
}

function groupHint(
  group: MenuModifierGroup,
  t: ReturnType<typeof useTranslations<"product">>,
): string {
  if (group.selection_type === "single") {
    return group.min_select > 0 ? t("chooseOneRequired") : t("chooseOne")
  }
  if (group.min_select > 0) return t("chooseAtLeast", { count: group.min_select })
  if (group.max_select > 0) return t("chooseUpTo", { count: group.max_select })
  return t("chooseMany")
}
