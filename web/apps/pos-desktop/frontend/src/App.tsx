import { useCallback, useEffect, useState } from 'react'
import {
  CloseCheck,
  GetCheck,
  ListCategories,
  ListCheckOrders,
  ListCheckPayments,
  ListOpenChecks,
  ListTables,
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
import { TablePlan } from './components/TablePlan'
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

  // Masa planı (Sprint-5 Wave 2) — full-panel floor plan shown instead of
  // ProductGrid while no adisyon is selected (see the center-panel toggle
  // below). layout_position is intentionally not read here — the plan
  // renders a grid, not a free-placement layout (see TableDTO's doc
  // comment).
  const [zones, setZones] = useState<main.ZonePlanDTO[]>([])
  const [tablesLoading, setTablesLoading] = useState(false)
  const [tablesError, setTablesError] = useState('')

  const [sendingOrder, setSendingOrder] = useState(false)
  const [receiptError, setReceiptError] = useState('')

  const [printer, setPrinter] = useState<PrinterEvent | null>(null)

  const canOpenCheck = Boolean(session?.branch_id)

  const refreshOpenChecks = useCallback(() => {
    ListOpenChecks()
      .then(setOpenChecks)
      .catch((err) => setReceiptError(describeError(err)))
  }, [])

  // Event-driven refetch (no WebSocket, no poll-by-default — see task note):
  // called after opening/closing a check, and on a 30s background timer
  // while the plan is actually visible (no adisyon selected). branchID is
  // read from session at call time rather than captured, since this is
  // reused across effects with different closures.
  const refreshTables = useCallback((branchID: string | undefined) => {
    if (!branchID) return
    setTablesLoading(true)
    ListTables(branchID)
      .then((z) => {
        setZones(z)
        setTablesError('')
      })
      .catch((err) => setTablesError(describeError(err)))
      .finally(() => setTablesLoading(false))
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
    refreshTables(session.branch_id)
  }, [session, refreshOpenChecks, refreshTables])

  // 30s background refresh — only while the plan is actually on screen (no
  // adisyon selected). Once a check is selected the center panel switches to
  // ProductGrid and there is nothing on screen for a stale plan to mislead;
  // the next open/close cycle re-syncs it anyway (see refreshTables calls in
  // handleSelectTable/handleOpenTakeaway/handleCloseCheck).
  useEffect(() => {
    if (!session?.authenticated || selectedCheck) return
    const id = setInterval(() => refreshTables(session.branch_id), 30_000)
    return () => clearInterval(id)
  }, [session, selectedCheck, refreshTables])

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
    setZones([])
    setTablesError('')
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

  // handleSelectTable is TablePlan's tap handler for an empty/reserved
  // table: open a new check bound to it. A 409 here means another station
  // occupied the table between the plan being drawn and this tap (the
  // backend's row lock — see pos/service.CheckService.Open's doc comment) —
  // refetch the plan so the card flips to "occupied" and becomes
  // join-able instead of leaving a stale "empty" card the cashier would tap
  // again.
  async function handleSelectTable(table: main.TableDTO) {
    if (!session?.branch_id) return
    setReceiptError('')
    try {
      const check = await OpenCheck(session.branch_id, table.id, table.name, '')
      refreshOpenChecks()
      refreshTables(session.branch_id)
      await handleSelectCheck(check.id)
    } catch (err) {
      setReceiptError(describeError(err))
      refreshTables(session.branch_id)
    }
  }

  // handleOpenTakeaway is CheckRail's "Paket servis" button — masasız satış,
  // the old free-text-table path's replacement now that table-bound
  // adisyon açma goes through TablePlan. tableID "" leaves the check's table
  // unset, matching the pre-Wave-2 TableLabel-only OpenCheck behavior.
  async function handleOpenTakeaway() {
    if (!session?.branch_id) return
    setReceiptError('')
    try {
      const check = await OpenCheck(session.branch_id, '', 'Paket servis', '')
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
      refreshTables(session?.branch_id)
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
          onOpenTakeaway={handleOpenTakeaway}
          canOpenCheck={canOpenCheck}
        />

        {selectedCheck ? (
          <ProductGrid categories={categories} disabled={!selectedCheck} onAddProduct={handleAddProduct} />
        ) : (
          <TablePlan
            zones={zones}
            loading={tablesLoading}
            errorMessage={tablesError}
            onSelectAvailable={handleSelectTable}
            onSelectOccupied={handleSelectCheck}
          />
        )}

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
