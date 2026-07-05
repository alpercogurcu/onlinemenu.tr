import { useState } from 'react'
import { ErrorBanner } from './ErrorBanner'

type LoginScreenProps = {
  onDevLogin: (email: string) => Promise<void>
  onKeycloakLogin: () => Promise<void>
  devLoginEnabled: boolean
  errorMessage: string
  keycloakLoading: boolean
}

export function LoginScreen({
  onDevLogin,
  onKeycloakLogin,
  devLoginEnabled,
  errorMessage,
  keycloakLoading,
}: LoginScreenProps) {
  const [email, setEmail] = useState('cashier@example.com')
  const [submitting, setSubmitting] = useState(false)

  async function handleDevSubmit(e: React.FormEvent) {
    e.preventDefault()
    setSubmitting(true)
    try {
      await onDevLogin(email)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-surface">
      <div className="w-full max-w-sm space-y-4 rounded-lg border border-line bg-panel p-8">
        <h1 className="font-display text-2xl font-bold text-ink">Kasa girişi</h1>

        <button
          type="button"
          onClick={() => void onKeycloakLogin()}
          disabled={keycloakLoading}
          className="min-h-14 w-full rounded-md bg-amber px-4 py-3 font-semibold text-amber-ink disabled:opacity-50"
        >
          {keycloakLoading ? 'Tarayıcıda giriş bekleniyor…' : 'Keycloak ile giriş'}
        </button>

        {devLoginEnabled && (
          <>
            <div className="relative py-2">
              <div className="absolute inset-0 flex items-center">
                <span className="w-full border-t border-line" />
              </div>
              <div className="relative flex justify-center text-xs uppercase">
                <span className="bg-panel px-2 text-ink-dim">veya (dev)</span>
              </div>
            </div>

            <form onSubmit={handleDevSubmit} className="space-y-4">
              <p className="text-sm text-ink-dim">
                İstasyon oturumu (dev-login) — yalnızca geliştirme ortamında görünür.
              </p>
              <label className="block text-sm text-ink-dim" htmlFor="email">
                E-posta
              </label>
              <input
                id="email"
                className="min-h-14 w-full rounded-md border border-line bg-surface px-3 py-2 text-ink"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
              <button
                type="submit"
                disabled={submitting}
                className="min-h-14 w-full rounded-md border border-line px-4 py-3 font-semibold text-ink disabled:opacity-50"
              >
                {submitting ? 'Giriş yapılıyor…' : 'Giriş yap (dev)'}
              </button>
            </form>
          </>
        )}

        <ErrorBanner message={errorMessage} />
      </div>
    </div>
  )
}
