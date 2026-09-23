"use client"

import * as DialogPrimitive from "@radix-ui/react-dialog"
import { XIcon } from "lucide-react"
import type { ReactNode } from "react"

import { cn } from "@/lib/utils"

interface TouchSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  /** Right side of the header, before the close button (e.g. a price). */
  aside?: ReactNode
  closeLabel: string
  /** "adaptive": bottom sheet on a phone, centred dialog from md up. */
  layout?: "adaptive" | "bottom"
  footer?: ReactNode
  children: ReactNode
  describedBy?: string
}

/**
 * Touch-first modal for the order screen. The admin's shadcn Dialog/Sheet
 * have a 16px close glyph and desktop spacing; a waiter on a phone needs the
 * panel where the thumb is (bottom), a 48px close target and a footer that
 * never scrolls away. One component, CSS-only breakpoint switch, so there is
 * no media-query state to get out of sync with the layout.
 */
export function TouchSheet({
  open,
  onOpenChange,
  title,
  aside,
  closeLabel,
  layout = "adaptive",
  footer,
  children,
  describedBy,
}: TouchSheetProps) {
  const adaptive = layout === "adaptive"
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 fixed inset-0 z-50 bg-black/50" />
        <DialogPrimitive.Content
          aria-describedby={describedBy}
          className={cn(
            "bg-background fixed z-50 flex flex-col overflow-hidden border shadow-lg outline-none",
            "data-[state=open]:animate-in data-[state=closed]:animate-out motion-reduce:animate-none",
            // Phone: bottom sheet, thumb-reachable, leaves the top of the
            // screen visible so the waiter keeps context.
            "inset-x-0 bottom-0 max-h-[88dvh] rounded-t-2xl",
            "data-[state=open]:slide-in-from-bottom data-[state=closed]:slide-out-to-bottom data-[state=open]:duration-200 data-[state=closed]:duration-150",
            adaptive &&
              "md:inset-auto md:top-1/2 md:left-1/2 md:max-h-[85dvh] md:w-full md:max-w-xl md:-translate-x-1/2 md:-translate-y-1/2 md:rounded-2xl md:data-[state=open]:slide-in-from-bottom-4 md:data-[state=open]:fade-in-0 md:data-[state=closed]:fade-out-0",
            !adaptive && "md:mx-auto md:max-w-xl",
          )}
        >
          {/* Grab handle: tells a phone user this is a sheet over the page. */}
          <div aria-hidden="true" className={cn("mx-auto mt-2 h-1.5 w-10 shrink-0 rounded-full bg-muted", adaptive && "md:hidden")} />
          <div className="flex shrink-0 items-center gap-3 border-b px-4 py-2">
            <DialogPrimitive.Title className="min-w-0 flex-1 text-lg leading-tight font-bold break-words">
              {title}
            </DialogPrimitive.Title>
            {aside}
            <DialogPrimitive.Close
              aria-label={closeLabel}
              className="text-muted-foreground hover:bg-accent focus-visible:ring-ring/50 -mr-2 flex size-12 shrink-0 items-center justify-center rounded-lg outline-none focus-visible:ring-[3px]"
            >
              <XIcon className="size-6" />
            </DialogPrimitive.Close>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">{children}</div>
          {footer && (
            <div className="shrink-0 border-t px-4 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))]">{footer}</div>
          )}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}

interface TouchConfirmProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  body: ReactNode
  cancelLabel: string
  confirmLabel: string
  closeLabel: string
  destructive?: boolean
  pending?: boolean
  onConfirm: () => void
}

/** Two-button question with ≥56px targets; the safe choice sits on the left. */
export function TouchConfirm({
  open,
  onOpenChange,
  title,
  body,
  cancelLabel,
  confirmLabel,
  closeLabel,
  destructive,
  pending,
  onConfirm,
}: TouchConfirmProps) {
  return (
    <TouchSheet
      open={open}
      onOpenChange={(next) => {
        if (!pending) onOpenChange(next)
      }}
      title={title}
      closeLabel={closeLabel}
      footer={
        <div className="flex gap-3">
          <button
            type="button"
            disabled={pending}
            onClick={() => onOpenChange(false)}
            className="min-h-14 flex-1 rounded-xl border-2 text-base font-semibold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50"
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            disabled={pending}
            aria-busy={pending}
            onClick={onConfirm}
            className={cn(
              "min-h-14 flex-1 rounded-xl text-base font-bold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50",
              destructive ? "bg-destructive text-white" : "bg-primary text-primary-foreground",
            )}
          >
            {confirmLabel}
          </button>
        </div>
      }
    >
      <div className="p-4 text-base">{body}</div>
    </TouchSheet>
  )
}
