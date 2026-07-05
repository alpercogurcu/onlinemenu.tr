import { useCallback, useEffect, useState } from 'react'
import {
  CloseCheck,
  GetCheck,
  ListCategories,
  ListCheckOrders,
  ListCheckPayments,
  ListOpenChecks,
  Login,
  Logout,
  OpenCheck,
  PlaceOrder,
  RegisterCashPayment,
  WhoAmI,
} from '../wailsjs/go/main/App'
import { EventsOn } from '../wailsjs/runtime/runtime'
import type { main } from '../wailsjs/go/models'
import { CheckRail } from './components/CheckRail'
import { ProductGrid } from './components/ProductGrid'
import { Receipt } from './components/Receipt'
import { LoginScreen } from './components/LoginScreen'
import {
  addProductToPending,
  confirmedOrdersTotal,
  pendingTotal as sumPendingTotal,
  removePendingLine,
  toOrderItemInputs,
  type PendingLine,
} from './lib/cart'
import { describeError } from './lib/errors'

type PrinterEvent = {
  kind: string
  status: 'connected' | 'disconnected' | 'error'
  error?: string
}

function App() {
  const [session, setSession] = useState<main.SessionDTO | null>(null)
  const [authChecked, setAuthChecked] = useState(false)
  const [authError, setAuthError] = useState('')

  const [categories, setCategories] = useState<main.CategoryDTO[]>([])
  const [openChecks, setOpenChecks] = useState<main.CheckDTO[]>([])
  const [selectedCheck, setSelectedCheck] = useState<main.CheckDTO | null>(null)
  const [confirmedOrders, setConfirmedOrders] = useState<main.OrderDTO[]>([])
  const [pendingLines, setPendingLines] = useState<PendingLine[]>([])
  const [paidChecks, setPaidChecks] = useState<Record<string, boolean>>({})
  const [alreadyPaidTotal, setAlreadyPaidTotal] = useState(0)

  const [sendingOrder, setSendingOrder] = useState(false)
  const [receiptError, setReceiptError] = useState('')

  const [printer, setPrinter] = useState<PrinterEvent | null>(null)

  const canOpenCheck = Boolean(session?.branch_id)

  const refreshOpenChecks = useCallback(() => {
    ListOpenChecks()
      .then(setOpenChecks)
      .catch((err) => setReceiptError(describeError(err)))
  }, [])

  useEffect(() => {
    WhoAmI()
      .then((s) => setSession(s.authenticated ? s : null))
      .catch(() => setSession(null))
      .finally(() => setAuthChecked(true))

    const unsubscribe = EventsOn('hardware:printer', (evt: PrinterEvent) => setPrinter(evt))
    return () => unsubscribe()
  }, [])

  useEffect(() => {
    if (!session?.authenticated) return
    ListCategories()
      .then(setCategories)
      .catch((err) => setReceiptError(describeError(err)))
    refreshOpenChecks()
  }, [session, refreshOpenChecks])

  async function handleLogin(email: string) {
    try {
      const result = await Login(email)
      setSession(result)
      setAuthError('')
    } catch (err) {
      setAuthError(describeError(err))
    }
  }

  async function handleLogout() {
    await Logout()
    setSession(null)
    setSelectedCheck(null)
    setConfirmedOrders([])
    setPendingLines([])
    setOpenChecks([])
    setAlreadyPaidTotal(0)
  }

  async function handleSelectCheck(checkId: string) {
    setReceiptError('')
    setPendingLines([])
    setAlreadyPaidTotal(0)
    try {
      const [check, orders] = await Promise.all([GetCheck(checkId), ListCheckOrders(checkId)])
      setSelectedCheck(check)
      setConfirmedOrders(orders)
    } catch (err) {
      setReceiptError(describeError(err))
      return
    }

    // Double-payment guard (see pos.go's ListCheckPayments doc comment): a
    // check reopened after a payment was already recorded (e.g. the app
    // restarted between RegisterCashPayment and CloseCheck) must not be
    // offered "Nakit al" again.
    //
    // IMPORTANT: this is a UI-only guard — the backend's CloseCheck does not
    // reject overpayment, only underpayment, so this frontend check is the
    // only thing standing between a reopened check and a double payment.
    // It is also currently inert for a plain "cashier" session: this call
    // needs "payment.payment.read", which cashier does not have (see the
    // pos.go doc comment for why that's not a one-line permission grant).
    // The read is fail-open on ANY error (403 included) so a permission gap
    // or a transient failure never blocks selecting the check — it just
    // means the cashier won't see this line or get this guard.
    try {
      const payments = await ListCheckPayments(checkId)
      const paidSoFar = payments.reduce((sum, p) => sum + p.amount_total, 0)
      setAlreadyPaidTotal(paidSoFar)
      if (paidSoFar > 0) {
        setPaidChecks((paid) => ({ ...paid, [checkId]: true }))
      }
    } catch (err) {
      // Fail-open — see comment above. Logged (not surfaced to
      // receiptError) so the gap is visible in devtools without
      // interrupting the cashier's flow.
      console.warn('ListCheckPayments failed — double-payment guard inactive for this check', err)
    }
  }

  async function handleOpenCheck(tableLabel: string) {
    if (!session?.branch_id) return
    try {
      const check = await OpenCheck(session.branch_id, tableLabel, '')
      refreshOpenChecks()
      await handleSelectCheck(check.id)
    } catch (err) {
      setReceiptError(describeError(err))
    }
  }

  function handleAddProduct(product: main.ProductDTO) {
    if (!selectedCheck) return
    setPendingLines((lines) => addProductToPending(lines, product))
  }

  function handleRemovePendingLine(clientId: string) {
    setPendingLines((lines) => removePendingLine(lines, clientId))
  }

  async function handleSendOrder() {
    if (!selectedCheck || !session?.branch_id || pendingLines.length === 0) return
    setSendingOrder(true)
    setReceiptError('')
    try {
      const order = await PlaceOrder(session.branch_id, selectedCheck.id, toOrderItemInputs(pendingLines))
      setConfirmedOrders((orders) => [...orders, order])
      setPendingLines([])
    } catch (err) {
      setReceiptError(describeError(err))
    } finally {
      setSendingOrder(false)
    }
  }

  async function handleRegisterPayment(amountTotal: number) {
    if (!selectedCheck || !session?.branch_id) return
    setReceiptError('')
    try {
      const payment = await RegisterCashPayment(session.branch_id, selectedCheck.id, amountTotal)
      setPaidChecks((paid) => ({ ...paid, [selectedCheck.id]: true }))
      setAlreadyPaidTotal((total) => total + payment.amount_total)
    } catch (err) {
      setReceiptError(describeError(err))
      throw err
    }
  }

  async function handleCloseCheck() {
    if (!selectedCheck) return
    setReceiptError('')
    try {
      await CloseCheck(selectedCheck.id)
      setSelectedCheck(null)
      setConfirmedOrders([])
      setPendingLines([])
      setAlreadyPaidTotal(0)
      refreshOpenChecks()
    } catch (err) {
      setReceiptError(describeError(err))
    }
  }

  if (!authChecked) {
    return <div className="flex min-h-screen items-center justify-center bg-surface text-ink-dim">Yükleniyor…</div>
  }

  if (!session?.authenticated) {
    return <LoginScreen onLogin={handleLogin} errorMessage={authError} />
  }

  return (
    <div className="flex h-screen flex-col bg-surface text-ink">
      <header className="flex min-h-12 shrink-0 items-center justify-between border-b border-line px-4 text-sm">
        <span>
          {session.full_name} ({session.email})
        </span>
        <div className="flex items-center gap-4">
          <span className="text-ink-dim">
            Yazıcı: {printer ? `${printer.status}${printer.error ? ` — ${printer.error}` : ''}` : 'bekleniyor…'}
          </span>
          <button type="button" onClick={handleLogout} className="min-h-8 rounded px-2 text-ink-dim">
            Çıkış
          </button>
        </div>
      </header>

      <div className="flex flex-1 overflow-hidden">
        <CheckRail
          checks={openChecks}
          selectedCheckId={selectedCheck?.id ?? null}
          onSelect={handleSelectCheck}
          onOpenCheck={handleOpenCheck}
          canOpenCheck={canOpenCheck}
        />

        <ProductGrid categories={categories} disabled={!selectedCheck} onAddProduct={handleAddProduct} />

        <Receipt
          tableLabel={selectedCheck?.table_label ?? ''}
          confirmedOrders={confirmedOrders}
          pendingLines={pendingLines}
          onRemovePendingLine={handleRemovePendingLine}
          onSendOrder={handleSendOrder}
          sendingOrder={sendingOrder}
          confirmedTotal={confirmedOrdersTotal(confirmedOrders)}
          pendingTotal={sumPendingTotal(pendingLines)}
          hasPaid={selectedCheck ? Boolean(paidChecks[selectedCheck.id]) : false}
          alreadyPaidTotal={alreadyPaidTotal}
          onRegisterPayment={handleRegisterPayment}
          onCloseCheck={handleCloseCheck}
          errorMessage={receiptError}
        />
      </div>
    </div>
  )
}

export default App
