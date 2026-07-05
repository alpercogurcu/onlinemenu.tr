import type { ReactNode } from 'react'
import { useHoldToConfirm } from '../hooks/useHoldToConfirm'

type Variant = 'amber' | 'danger'

const VARIANT_CLASSES: Record<Variant, { base: string; ring: string }> = {
  amber: {
    base: 'bg-amber text-amber-ink',
    ring: 'stroke-amber-ink/70',
  },
  // Red is reserved exclusively for void/cancel — no other element in this
  // app uses it (design plan: meaning-preserving color).
  danger: {
    base: 'bg-panel text-danger border border-danger',
    ring: 'stroke-danger',
  },
}

type HoldButtonProps = {
  label: string
  holdingLabel?: string
  variant?: Variant
  disabled?: boolean
  onConfirm: () => void
  icon?: ReactNode
}

/**
 * Hold-to-confirm action button (600ms) — replaces confirmation modals for
 * destructive/final actions (close check, cancel check/line). A ring drawn
 * around the button fills as the press is held; releasing early cancels
 * with no side effect. Disabled under prefers-reduced-motion via CSS only
 * (the hold itself is unaffected — see useHoldToConfirm).
 */
export function HoldButton({
  label,
  holdingLabel,
  variant = 'amber',
  disabled = false,
  onConfirm,
  icon,
}: HoldButtonProps) {
  const { progress, holding, handlers } = useHoldToConfirm(onConfirm)
  const classes = VARIANT_CLASSES[variant]

  const circumference = 2 * Math.PI * 46
  const dashoffset = circumference * (1 - progress)

  return (
    <button
      type="button"
      disabled={disabled}
      className={`relative flex min-h-14 w-full select-none items-center justify-center gap-2 rounded-lg px-4 py-3 text-base font-semibold transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${classes.base}`}
      {...(disabled ? {} : handlers)}
    >
      {!disabled && (
        <svg
          className="pointer-events-none absolute inset-0 h-full w-full"
          viewBox="0 0 100 100"
          preserveAspectRatio="none"
          aria-hidden="true"
        >
          <circle
            cx="50"
            cy="50"
            r="46"
            fill="none"
            strokeWidth="4"
            className={classes.ring}
            strokeDasharray={circumference}
            strokeDashoffset={dashoffset}
            strokeLinecap="round"
            style={{ transformOrigin: '50% 50%', transform: 'rotate(-90deg)' }}
          />
        </svg>
      )}
      {icon}
      <span>{holding && holdingLabel ? holdingLabel : label}</span>
    </button>
  )
}
