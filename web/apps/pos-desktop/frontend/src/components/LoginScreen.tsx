import { useState } from 'react'
import { ErrorBanner } from './ErrorBanner'

type LoginScreenProps = {
  onLogin: (email: string) => Promise<void>
  errorMessage: string
}

export function LoginScreen({ onLogin, errorMessage }: LoginScreenProps) {
  const [email, setEmail] = useState('cashier@example.com')
  const [submitting, setSubmitting] = useState(false)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setSubmitting(true)
    try {
      await onLogin(email)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-surface">
      <form
        onSubmit={handleSubmit}
        className="w-full max-w-sm space-y-4 rounded-lg border border-line bg-panel p-8"
      >
        <h1 className="font-display text-2xl font-bold text-ink">Kasa girişi</h1>
        <p className="text-sm text-ink-dim">
          İstasyon oturumu (dev-login) — gerçek Keycloak girişi bu ekranın yerini alacak.
        </p>
        <label className="block text-sm text-ink-dim" htmlFor="email">
          E-posta
        </label>
        <input
          id="email"
          className="min-h-14 w-full rounded-md border border-line bg-surface px-3 py-2 text-ink"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          autoFocus
        />
        <button
          type="submit"
          disabled={submitting}
          className="min-h-14 w-full rounded-md bg-amber px-4 py-3 font-semibold text-amber-ink disabled:opacity-50"
        >
          {submitting ? 'Giriş yapılıyor…' : 'Giriş yap'}
        </button>
        <ErrorBanner message={errorMessage} />
      </form>
    </div>
  )
}
