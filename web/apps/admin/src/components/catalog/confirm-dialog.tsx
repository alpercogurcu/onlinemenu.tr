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
  // Required — Türkçe UI yalnız next-intl üzerinden gelir (see CLAUDE.md's
  // dil kuralı), so this can't default to a hardcoded string; every call
  // site must pass its own translated label (catalog.common.cancel or an
  // equivalent).
  cancelLabel: string
  destructive?: boolean
  onConfirm: () => Promise<void> | void
  onError?: (err: unknown) => void
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
  cancelLabel,
  destructive = false,
  onConfirm,
  onError,
  secondaryAction,
}: ConfirmDialogProps) {
  const [isPending, setIsPending] = React.useState(false)

  const handleConfirm = async (e: React.MouseEvent) => {
    e.preventDefault()
    setIsPending(true)
    try {
      await onConfirm()
      onOpenChange(false)
    } catch (err) {
      // Keep the dialog open on rejection — the caller's mutation already
      // toasts the failure; onError just lets a caller observe it too.
      onError?.(err)
    } finally {
      setIsPending(false)
    }
  }

  const handleSecondary = () => {
    secondaryAction?.onClick()
    onOpenChange(false)
  }

  // Radix closes the AlertDialog root on Escape (and would on outside
  // interaction) regardless of the confirm button's disabled state, so a
  // pending confirm must veto close requests here too, not just via the
  // button's disabled attribute.
  const handleOpenChange = (next: boolean) => {
    if (isPending && !next) {
      return
    }
    onOpenChange(next)
  }

  return (
    <AlertDialog open={open} onOpenChange={handleOpenChange}>
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
