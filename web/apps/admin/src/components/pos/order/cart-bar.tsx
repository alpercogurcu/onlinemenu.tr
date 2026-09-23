"use client"

import { ChevronUp, CircleAlert, Loader2, RotateCcw, Send, ShoppingBasket } from "lucide-react"
import { useTranslations } from "next-intl"

import { formatMoney } from "@onlinemenu/pos-core"

import type { OrderError } from "@/lib/pos-order"
import { isRetryable } from "@/lib/pos-order"
import { cn } from "@/lib/utils"

interface SendButtonProps {
  count: number
  sending: boolean
  onSend: () => void
  className?: string
}

export function SendButton({ count, sending, onSend, className }: SendButtonProps) {
  const t = useTranslations("posOrder.cart")
  return (
    <button
      type="button"
      onClick={onSend}
      disabled={count === 0 || sending}
      aria-busy={sending}
      className={cn(
        "bg-primary text-primary-foreground flex min-h-14 items-center justify-center gap-2 rounded-xl px-3 text-base font-bold whitespace-nowrap sm:text-lg",
        "outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 active:scale-[0.99] disabled:opacity-50 motion-reduce:active:scale-100",
        className,
      )}
    >
      {sending ? <Loader2 className="size-5 animate-spin" aria-hidden="true" /> : <Send className="size-5" aria-hidden="true" />}
      {sending ? t("sending") : t("send")}
    </button>
  )
}

interface SendErrorProps {
  error: OrderError | null
  onRetry: () => void
  sending: boolean
}

/** Error right next to the send action (not a vanishing toast): cause + the way out. */
export function SendError({ error, onRetry, sending }: SendErrorProps) {
  const t = useTranslations("posOrder")
  if (!error) return null
  return (
    <div
      role="alert"
      className="border-status-danger-border bg-status-danger-bg text-status-danger-fg flex flex-wrap items-center gap-x-2 gap-y-2 rounded-xl border px-3 py-2"
    >
      <CircleAlert className="size-5 shrink-0 self-start" aria-hidden="true" />
      <p className="min-w-0 flex-1 basis-40 text-base font-medium">{error.message}</p>
      {isRetryable(error) && (
        <button
          type="button"
          onClick={onRetry}
          disabled={sending}
          className="border-status-danger-border ml-auto flex min-h-12 shrink-0 items-center gap-1.5 rounded-lg border bg-background px-4 text-base font-semibold text-foreground disabled:opacity-50"
        >
          <RotateCcw className="size-4" aria-hidden="true" />
          {t("retry")}
        </button>
      )}
    </div>
  )
}

interface CartBarProps {
  count: number
  total: number
  sending: boolean
  error: OrderError | null
  onOpenCart: () => void
  onSend: () => void
  onRetry: () => void
}

/**
 * Phone/tablet sticky bar: "N kalem · TUTAR" opens the cart, "Mutfağa gönder"
 * sends right away — the common case (everything is right) costs one tap.
 * Sticky, not fixed, so it sits inside the content column and never covers the
 * sidebar on a tablet; safe-area padding keeps it off the iOS home indicator.
 */
export function CartBar({ count, total, sending, error, onOpenCart, onSend, onRetry }: CartBarProps) {
  const t = useTranslations("posOrder.cart")
  return (
    <div className="bg-background/95 supports-[backdrop-filter]:bg-background/80 sticky bottom-0 z-20 -mx-4 mt-auto space-y-2 border-t px-4 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] backdrop-blur lg:hidden">
      <SendError error={error} onRetry={onRetry} sending={sending} />
      <div className="flex items-stretch gap-3">
        <button
          type="button"
          onClick={onOpenCart}
          aria-label={t("open")}
          aria-haspopup="dialog"
          className="bg-card flex min-h-14 min-w-0 flex-1 items-center gap-3 rounded-xl border-2 px-3 text-left outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
        >
          <span className="relative shrink-0">
            <ShoppingBasket className="size-6" aria-hidden="true" />
            {count > 0 && (
              <span className="bg-primary text-primary-foreground absolute -top-2 -right-2.5 flex h-5 min-w-5 items-center justify-center rounded-full px-1 text-xs font-bold tabular-nums">
                {count}
              </span>
            )}
          </span>
          <span className="min-w-0 flex-1 leading-tight">
            <span className="block text-sm text-muted-foreground" data-testid="cart-count">
              {t("summary", { count })}
            </span>
            <span className="block text-lg font-bold tabular-nums" data-testid="cart-total">
              {formatMoney(total)}
            </span>
          </span>
          <ChevronUp className="size-5 shrink-0 text-muted-foreground" aria-hidden="true" />
        </button>
        <SendButton count={count} sending={sending} onSend={onSend} className="flex-[1.3]" />
      </div>
    </div>
  )
}
