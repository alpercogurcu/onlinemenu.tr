// Lucide icons, inlined as SVG rather than pulled from `lucide-react`.
//
// This app ships no icon library at all today (status is carried by text, CSS
// patterns and the receipt's torn edge — see style.css), and Wails runs fully
// offline, so adding a ~30kB runtime dependency to render four glyphs would be
// a poor trade. These are the upstream Lucide 0.4x path definitions verbatim
// (ISC licensed), with Lucide's own default attributes.
//
// If more icons are needed, adding `lucide-react` and deleting this file is the
// right call — flagged to team-lead.

type IconProps = {
  className?: string
  /** Icons here are always paired with visible text (WCAG: status is never
   * color- or icon-only), so they are decorative by default. */
  size?: number
}

function Svg({ className, size = 16, children }: IconProps & { children: React.ReactNode }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
      focusable="false"
    >
      {children}
    </svg>
  )
}

/** lucide:clock — mali kayıt bekleniyor */
export function ClockIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <circle cx="12" cy="12" r="10" />
      <polyline points="12 6 12 12 16 14" />
    </Svg>
  )
}

/** lucide:check — fiş kesildi */
export function CheckIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <path d="M20 6 9 17l-5-5" />
    </Svg>
  )
}

/** lucide:triangle-alert — mali kayıt başarısız */
export function TriangleAlertIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3" />
      <path d="M12 9v4" />
      <path d="M12 17h.01" />
    </Svg>
  )
}

/** lucide:ban — fiş iptal edildi */
export function BanIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <circle cx="12" cy="12" r="10" />
      <path d="m4.9 4.9 14.2 14.2" />
    </Svg>
  )
}

/** lucide:eye-off — durum okunamıyor (403: bu rol ödeme durumunu okuyamaz) */
export function EyeOffIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <path d="M10.733 5.076a10.744 10.744 0 0 1 11.205 6.575 1 1 0 0 1 0 .696 10.747 10.747 0 0 1-1.444 2.49" />
      <path d="M14.084 14.158a3 3 0 0 1-4.242-4.242" />
      <path d="M17.479 17.499a10.75 10.75 0 0 1-15.417-5.151 1 1 0 0 1 0-.696 10.75 10.75 0 0 1 4.446-5.143" />
      <path d="m2 2 20 20" />
    </Svg>
  )
}
