import { useCallback, useEffect, useRef } from 'react'

export const LONG_PRESS_MS = 500

/**
 * Tap vs. long press on one touch target. The click that follows a long press
 * is swallowed — otherwise "select the whole row" would be undone by the
 * row's own tap cycle a moment later. A scroll gesture on the rail arrives as
 * pointercancel and drops the pending long press.
 */
export function useLongPress(onTap: () => void, onLongPress: () => void) {
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const firedRef = useRef(false)

  const clear = useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current)
      timerRef.current = null
    }
  }, [])

  useEffect(() => clear, [clear])

  return {
    onPointerDown: () => {
      clear()
      firedRef.current = false
      timerRef.current = setTimeout(() => {
        timerRef.current = null
        firedRef.current = true
        onLongPress()
      }, LONG_PRESS_MS)
    },
    onPointerUp: clear,
    onPointerLeave: clear,
    onPointerCancel: clear,
    onClick: () => {
      if (firedRef.current) {
        firedRef.current = false
        return
      }
      onTap()
    },
    // A touch long press would otherwise open the webview's context menu.
    onContextMenu: (event: { preventDefault: () => void }) => event.preventDefault(),
  }
}
