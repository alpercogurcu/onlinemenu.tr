"use client"

import { MinusIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { useTranslations } from "next-intl"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useState } from "react"

import { Button } from "@onlinemenu/ui-kit"

import { useProblemMessage } from "@/components/error-state"
import { usePlaceOrder } from "@/hooks/use-storefront"
import { toProblem, type ApiProblem } from "@/lib/api"
import {
  MAX_CART_LINES,
  MAX_LINE_QUANTITY,
  cartTotal,
  lineTotal,
  toPlaceOrderRequest,
  useCartStore,
  type CartLine,
} from "@/lib/cart-store"
import { formatKurus } from "@/lib/money"

const MAX_NOTE_LENGTH = 500

export function CartScreen() {
  const t = useTranslations("cart")
  const router = useRouter()
  const problemMessage = useProblemMessage()

  const lines = useCartStore((s) => s.lines)
  const orderNote = useCartStore((s) => s.orderNote)
  const setOrderNote = useCartStore((s) => s.setOrderNote)
  const clear = useCartStore((s) => s.clear)
  const beginSubmission = useCartStore((s) => s.beginSubmission)
  const endSubmission = useCartStore((s) => s.endSubmission)

  const [problem, setProblem] = useState<ApiProblem | null>(null)
  const placeOrder = usePlaceOrder()

  if (lines.length === 0) {
    return (
      <div className="py-16 text-center">
        <p className="text-base font-medium">{t("empty")}</p>
        <p className="text-muted-foreground mt-1 text-sm">{t("emptyHint")}</p>
        <Button asChild size="touch" variant="outline" className="mt-6">
          <Link href="/menu">{t("backToMenu")}</Link>
        </Button>
      </div>
    )
  }

  const total = cartTotal(lines)
  const tooManyLines = lines.length > MAX_CART_LINES

  function handleSubmit() {
    setProblem(null)

    // The key is created here, ONCE, and reused by every retry of this same
    // cart (ADR-SEC-003). Creating it inside the mutation would give a
    // timed-out request a fresh key on retry, and the diner would be served —
    // and charged for — two orders.
    const idempotencyKey = beginSubmission()

    placeOrder.mutate(
      { body: toPlaceOrderRequest(lines, orderNote), idempotencyKey },
      {
        onSuccess: (result) => {
          endSubmission()
          clear()
          router.replace(`/orders/${result.order_id}?placed=1`)
        },
        onError: (error) => {
          const next = toProblem(error)
          setProblem(next)
          // A retriable failure (429, 5xx, no response at all) may have been
          // applied server-side, so the key is KEPT: retrying with it replays
          // the original answer instead of placing a second order. A 4xx is
          // final for this body — the key is burned and the next attempt,
          // after the diner edits the cart, needs a fresh one.
          if (!next.retriable) endSubmission()
        },
      },
    )
  }

  return (
    <div className="py-4">
      <ul className="flex flex-col gap-2">
        {lines.map((line) => (
          <li key={line.key}>
            <CartLineRow line={line} />
          </li>
        ))}
      </ul>

      <div className="mt-6">
        <label htmlFor="order-note" className="text-sm font-medium">
          {t("orderNote")}
        </label>
        <input
          id="order-note"
          type="text"
          maxLength={MAX_NOTE_LENGTH}
          value={orderNote}
          onChange={(event) => setOrderNote(event.target.value)}
          placeholder={t("orderNotePlaceholder")}
          className="border-input bg-background focus-visible:ring-ring/50 mt-2 h-11 w-full rounded-lg border px-3 text-base outline-none focus-visible:ring-[3px]"
        />
      </div>

      {tooManyLines ? (
        <p className="text-destructive mt-4 text-sm" role="alert">
          {t("tooManyLines")}
        </p>
      ) : null}

      {problem === null ? null : (
        <p className="text-destructive mt-4 text-sm" role="alert">
          {problemMessage(problem)}
        </p>
      )}

      <div className="fixed inset-x-0 bottom-0 z-40 px-4 pb-[calc(env(safe-area-inset-bottom)+1rem)]">
        <div className="bg-background mx-auto w-full max-w-screen-sm rounded-2xl border p-3 shadow-lg">
          <div className="mb-2 flex items-baseline justify-between">
            <span className="text-sm font-medium">{t("total")}</span>
            <span className="text-lg font-semibold tabular-nums">{formatKurus(total)}</span>
          </div>
          {/* The cart total is indicative: the server re-derives every unit
              price from the menu read model and its answer is the real one. */}
          <p className="text-muted-foreground mb-3 text-xs">{t("totalHint")}</p>
          <Button
            size="touch-lg"
            className="w-full"
            disabled={placeOrder.isPending || tooManyLines}
            onClick={handleSubmit}
          >
            {placeOrder.isPending ? t("submitting") : t("submit")}
          </Button>
        </div>
      </div>
    </div>
  )
}

function CartLineRow({ line }: { line: CartLine }) {
  const t = useTranslations("common")
  const setQuantity = useCartStore((s) => s.setQuantity)
  const removeLine = useCartStore((s) => s.removeLine)

  return (
    <div className="bg-card flex items-start gap-3 rounded-xl border p-3">
      <div className="min-w-0 flex-1">
        <span className="block text-base font-medium">{line.productName}</span>
        {line.modifiers.length === 0 ? null : (
          <p className="text-muted-foreground mt-0.5 text-sm">
            {line.modifiers.map((modifier) => modifier.name).join(", ")}
          </p>
        )}
        {line.note === "" ? null : (
          <p className="text-muted-foreground mt-0.5 text-sm italic">{line.note}</p>
        )}
        <span className="mt-1 block text-base font-semibold tabular-nums">
          {formatKurus(lineTotal(line))}
        </span>
      </div>

      <div className="flex flex-col items-end gap-2">
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={t("remove")}
          onClick={() => removeLine(line.key)}
        >
          <Trash2Icon />
        </Button>
        <div className="flex items-center gap-1 rounded-lg border p-0.5">
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={t("decrease")}
            onClick={() => setQuantity(line.key, line.quantity - 1)}
          >
            <MinusIcon />
          </Button>
          <span className="w-6 text-center text-sm font-semibold tabular-nums">
            {line.quantity}
          </span>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={t("increase")}
            disabled={line.quantity >= MAX_LINE_QUANTITY}
            onClick={() => setQuantity(line.key, line.quantity + 1)}
          >
            <PlusIcon />
          </Button>
        </div>
      </div>
    </div>
  )
}
