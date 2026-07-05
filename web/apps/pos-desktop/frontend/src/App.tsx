import { useEffect, useState } from 'react'
import { Login, Logout, Ping, WhoAmI } from '../wailsjs/go/main/App'
import { EventsOn } from '../wailsjs/runtime/runtime'
import type { main } from '../wailsjs/go/models'

// This screen is a walking-skeleton placeholder, not a UI-wave deliverable.
// It exists only to prove the binding pipeline end to end:
//   webview -> Wails binding -> Go APIClient -> backend.
// The webview NEVER performs HTTP itself — every action below calls a Go
// method exposed via `Bind` in main.go. See README.md "Tek token-refresh
// otoritesi" for why this boundary is load-bearing, not stylistic.

type PrinterEvent = {
  kind: string
  status: 'connected' | 'disconnected' | 'error'
  error?: string
}

function App() {
  const [session, setSession] = useState<main.SessionDTO | null>(null)
  const [email, setEmail] = useState('cashier@example.com')
  const [pingStatus, setPingStatus] = useState<'idle' | 'ok' | 'error'>('idle')
  const [message, setMessage] = useState('')
  const [printer, setPrinter] = useState<PrinterEvent | null>(null)

  useEffect(() => {
    WhoAmI().then(setSession).catch((err) => setMessage(String(err)))

    const unsubscribe = EventsOn('hardware:printer', (evt: PrinterEvent) => {
      setPrinter(evt)
    })
    return () => unsubscribe()
  }, [])

  async function handlePing() {
    try {
      await Ping()
      setPingStatus('ok')
    } catch (err) {
      setPingStatus('error')
      setMessage(String(err))
    }
  }

  async function handleLogin() {
    try {
      const result = await Login(email)
      setSession(result)
      setMessage('')
    } catch (err) {
      setMessage(String(err))
    }
  }

  async function handleLogout() {
    await Logout()
    setSession(null)
  }

  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-4 p-8">
      <h1 className="text-xl font-semibold">onlinemenu.tr POS — iskelet</h1>

      <section className="w-full max-w-sm space-y-2 rounded border border-white/20 p-4">
        <h2 className="font-medium">Backend Bağlantısı</h2>
        <button className="rounded bg-white/10 px-3 py-1" onClick={handlePing}>
          Ping /healthz
        </button>
        <p>durum: {pingStatus}</p>
      </section>

      <section className="w-full max-w-sm space-y-2 rounded border border-white/20 p-4">
        <h2 className="font-medium">Oturum (dev-login)</h2>
        {session?.authenticated ? (
          <>
            <p>{session.full_name} ({session.email})</p>
            <button className="rounded bg-white/10 px-3 py-1" onClick={handleLogout}>
              Çıkış
            </button>
          </>
        ) : (
          <>
            <input
              className="w-full rounded bg-white/10 px-2 py-1 text-white"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
            <button className="rounded bg-white/10 px-3 py-1" onClick={handleLogin}>
              Giriş yap
            </button>
          </>
        )}
      </section>

      <section className="w-full max-w-sm space-y-2 rounded border border-white/20 p-4">
        <h2 className="font-medium">Donanım (mock printer)</h2>
        <p>
          {printer ? `${printer.kind}: ${printer.status}${printer.error ? ` (${printer.error})` : ''}` : 'olay bekleniyor…'}
        </p>
      </section>

      {message && <p className="text-red-400">{message}</p>}
    </div>
  )
}

export default App
