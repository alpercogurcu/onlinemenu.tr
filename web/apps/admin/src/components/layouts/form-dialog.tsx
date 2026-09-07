"use client"

import { XIcon } from "lucide-react"
import type { ReactNode } from "react"

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { cn } from "@/lib/utils"

const SIZE_CLASSES = {
  md: "sm:max-w-lg",
  lg: "sm:max-w-xl",
  xl: "sm:max-w-2xl",
} as const

interface FormDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description?: ReactNode
  size?: keyof typeof SIZE_CLASSES
  footer?: ReactNode
  // While true, Escape and outside-click cannot close the dialog and the
  // close button is disabled — used to keep a submit-in-flight dialog open.
  busy?: boolean
  children: ReactNode
}

export function FormDialog({
  open,
  onOpenChange,
  title,
  description,
  size = "md",
  footer,
  busy = false,
  children,
}: FormDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        showCloseButton={false}
        className={cn("gap-0 p-0", SIZE_CLASSES[size])}
        onEscapeKeyDown={(e) => {
          if (busy) e.preventDefault()
        }}
        onPointerDownOutside={(e) => {
          if (busy) e.preventDefault()
        }}
        onInteractOutside={(e) => {
          if (busy) e.preventDefault()
        }}
      >
        <DialogHeader className="px-6 pt-6">
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>

        <div className="max-h-[70vh] overflow-y-auto px-6 py-4">{children}</div>

        {footer ? (
          <div className="flex justify-end gap-2 border-t px-6 pt-4 pb-6">{footer}</div>
        ) : null}

        <button
          type="button"
          className="ring-offset-background focus:ring-ring absolute top-4 right-4 rounded-xs opacity-70 transition-opacity hover:opacity-100 focus:ring-2 focus:ring-offset-2 focus:outline-hidden disabled:pointer-events-none disabled:opacity-50"
          onClick={() => onOpenChange(false)}
          disabled={busy}
        >
          <XIcon className="size-4" />
          <span className="sr-only">Close</span>
        </button>
      </DialogContent>
    </Dialog>
  )
}
