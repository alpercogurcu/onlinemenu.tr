"use client"

import * as React from "react"
import type { ReactNode } from "react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"

interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: ReactNode
  confirmLabel: string
  cancelLabel?: string
  destructive?: boolean
  onConfirm: () => Promise<void> | void
  secondaryAction?: { label: string; onClick: () => void }
}

// Generic destructive-action gate (delete product, delete modifier group,
// "deactivate instead"...). The confirm button stays disabled for the whole
// lifetime of an async onConfirm so a second click can't fire a second
// request while the first is still in flight; AlertDialogAction closes on
// click by default (Radix Dialog.Close semantics), so the click handler
// calls preventDefault() to hold the dialog open until onConfirm settles,
// then closes it itself via onOpenChange.
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  cancelLabel = "Vazgeç",
  destructive = false,
  onConfirm,
  secondaryAction,
}: ConfirmDialogProps) {
  const [isPending, setIsPending] = React.useState(false)

  const handleConfirm = async (e: React.MouseEvent) => {
    e.preventDefault()
    setIsPending(true)
    try {
      await onConfirm()
      onOpenChange(false)
    } finally {
      setIsPending(false)
    }
  }

  const handleSecondary = () => {
    secondaryAction?.onClick()
    onOpenChange(false)
  }

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {description ? <AlertDialogDescription>{description}</AlertDialogDescription> : null}
        </AlertDialogHeader>
        <AlertDialogFooter>
          {secondaryAction ? (
            <Button type="button" variant="outline" disabled={isPending} onClick={handleSecondary}>
              {secondaryAction.label}
            </Button>
          ) : null}
          <AlertDialogCancel disabled={isPending}>{cancelLabel}</AlertDialogCancel>
          <AlertDialogAction
            variant={destructive ? "destructive" : "default"}
            disabled={isPending}
            onClick={handleConfirm}
          >
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
