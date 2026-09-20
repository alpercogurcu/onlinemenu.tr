import { useCallback, useRef, useState } from 'react'

const HOLD_DURATION_MS = 600

/**
 * A click event whose `detail` is 0 was not produced by a pointer: it comes
 * from Enter/Space on a focused button (or from assistive tech). A pointer
 * press must be held for HOLD_DURATION_MS, but a keyboard press is already a
 * deliberate act — there is no accidental graze to guard against — so it
 * confirms immediately.
 */
export function isKeyboardActivation(clickDetail: number): boolean {
  return clickDetail === 0
}

/**
 * Press-and-hold-to-confirm, per the design plan: closing/canceling a check
 * uses a 600ms hold (ring fills around the button) instead of a modal
 * dialog — releasing early cancels with no side effect. Used for both
 * "Kapat" (amber) and "İptal et" (red, void-only) actions.
 */
export function useHoldToConfirm(onConfirm: () => void) {
  const [progress, setProgress] = useState(0) // 0..1
  const [holding, setHolding] = useState(false)
  const rafRef = useRef<number | null>(null)
  const startRef = useRef<number>(0)

  const cancel = useCallback(() => {
    if (rafRef.current !== null) {
      cancelAnimationFrame(rafRef.current)
      rafRef.current = null
    }
    setHolding(false)
    setProgress(0)
  }, [])

  const start = useCallback(() => {
    setHolding(true)
    startRef.current = performance.now()

    const tick = (now: number) => {
      const elapsed = now - startRef.current
      const next = Math.min(1, elapsed / HOLD_DURATION_MS)
      setProgress(next)
      if (next >= 1) {
        rafRef.current = null
        setHolding(false)
        onConfirm()
        return
      }
      rafRef.current = requestAnimationFrame(tick)
    }
    rafRef.current = requestAnimationFrame(tick)
  }, [onConfirm])

  return {
    progress,
    holding,
    handlers: {
      onPointerDown: start,
      onPointerUp: cancel,
      onPointerLeave: cancel,
      onPointerCancel: cancel,
      onClick: (event: { detail: number }) => {
        if (isKeyboardActivation(event.detail)) onConfirm()
      },
    },
  }
}
