import type { main } from '../../wailsjs/go/models'
import { ErrorBanner } from './ErrorBanner'

type ContextPickerProps = {
  contexts: main.KeycloakContextDTO[]
  onSelect: (membershipId: string) => Promise<void>
  errorMessage: string
  loading: boolean
}

// Shown after LoginWithKeycloak resolves to more than one selectable
// tenant/branch membership (see app.go's KeycloakLoginResultDTO) — mirrors
// admin's auth/callback context-picker view (callback-client.tsx).
export function ContextPicker({ contexts, onSelect, errorMessage, loading }: ContextPickerProps) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-surface">
      <div className="w-full max-w-sm space-y-4 rounded-lg border border-line bg-panel p-8">
        <h1 className="font-display text-2xl font-bold text-ink">Bağlam seçin</h1>
        <p className="text-sm text-ink-dim">Devam etmek için işletme/şube seçin</p>

        <div className="space-y-2">
          {contexts.map((c) => (
            <button
              key={c.membership_id}
              type="button"
              disabled={loading}
              onClick={() => void onSelect(c.membership_id)}
              className="min-h-14 w-full rounded-md border border-line bg-surface px-4 py-3 text-left text-ink disabled:opacity-50"
            >
              {c.tenant_name}
              {c.branch_name ? ` · ${c.branch_name}` : ''} — {c.role_name}
            </button>
          ))}
        </div>

        <ErrorBanner message={errorMessage} />
      </div>
    </div>
  )
}
