/**
 * Error text treatment for this app. Deliberately NOT red: the design plan
 * reserves red exclusively for void/cancel ("başka hiçbir yerde kırmızı yok
 * — anlam koruması") and defines amber as money/primary-action and teal as
 * open-check status — none of the plan's four meaning-locked colors are
 * free for a generic error state. A bordered neutral box + explicit "Hata:"
 * label carries the distinction through shape/label instead of color, so
 * red keeps its single meaning everywhere in the app.
 */
export function ErrorBanner({ message }: { message: string }) {
  if (!message) return null
  return (
    <p className="rounded-md border border-line bg-surface px-3 py-2 text-sm text-ink" role="alert">
      <span className="font-semibold">Hata:</span> {message}
    </p>
  )
}
