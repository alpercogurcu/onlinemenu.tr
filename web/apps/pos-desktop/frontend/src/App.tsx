import { useCallback, useEffect, useState } from 'react'
import {
  CloseCheck,
  DevLoginEnabled,
  GetCheck,
  ListCategories,
  ListCheckOrders,
  ListCheckPayments,
  ListOpenChecks,
  ListTables,
  Login,
  LoginWithKeycloak,
  Logout,
  OpenCheck,
  PlaceOrder,
  PrinterStatus,
  PrintReceipt,
  RegisterCashPayment,
  SelectKeycloakContext,
  TryRestoreSession,
} from '../wailsjs/go/main/App'
import { EventsOn } from '../wailsjs/runtime/runtime'
import type { main } from '../wailsjs/go/models'
import { CheckRail } from './components/CheckRail'
import { ContextPicker } from './components/ContextPicker'
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
  const [devLoginEnabled, setDevLoginEnabled] = useState(false)
  const [keycloakLoading, setKeycloakLoading] = useState(false)
  const [pendingContexts, setPendingContexts] = useState<main.KeycloakContextDTO[]>([])

  const [categories, setCategories] = useState<main.CategoryDTO[]>([])
  const [openChecks, setOpenChecks] = useState<main.CheckDTO[]>([])
  const [selectedCheck, setSelectedCheck] = useState<main.CheckDTO | null>(null)
  const [confirmedOrders, setConfirmedOrders] = useState<main.OrderDTO[]>([])
  const [pendingLines, setPendingLines] = useState<PendingLine[]>([])
  // alreadyPaidTotal is the ONLY thing that drives whether a check is
  // "fully paid" (see Receipt.tsx's remainingBalance) — there is no
  // separate sticky per-check boolean anymore. It is seeded from
  // ListCheckPayments on selection (fail-open — see the try/catch in
  // handleSelectCheck) and accumulated locally on every successful
  // RegisterCashPayment, so a split payment made entirely within this
  // session is always correct even for a plain cashier session that
  // cannot re-read ListCheckPayments after the first call.
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

  // Receipt printing — lastReceivedAmount is the CUMULATIVE cash the
  // customer has physically handed over across every cash-payment step
  // taken for the currently selected check this session (Receipt.tsx's
  // onRegisterPayment receivedAmount, summed — not just the last step),
  // so a split payment's printed receipt still shows the correct total
  // "ALINAN"/change (internal/receipt.Build computes change as
  // receivedAmount - subtotal, so this must be the sum across every
  // installment, not only the final one). Reset to 0 on every check
  // selection (see handleSelectCheck) so it never leaks between checks.
  //
  // KNOWN LIMITATION (pre-existing, unchanged by this split-payment work):
  // if a check's payments were made in an earlier app session (e.g. the
  // station restarted mid-split), this session never learns what cash was
  // physically handed over for those — it only accumulates what THIS
  // session itself registers. A reprint after such a restart still shows
  // whatever partial amount this session collected, not the true total.
  const [lastReceivedAmount, setLastReceivedAmount] = useState(0)
  const [printError, setPrintError] = useState('')
  const [printRetryCheckId, setPrintRetryCheckId] = useState<string | null>(null)

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

  // TryRestoreSession subsumes the old WhoAmI-on-mount check: it first
  // attempts a silent Keycloak refresh-token-backed restore (see
  // app.go's doc comment), then falls back to the pre-existing dev-login
  // CTX-token-in-keychain path — one call, no ambiguity about which
  // session wins if a station has used both flows.
  useEffect(() => {
    TryRestoreSession()
      .then((s) => setSession(s.authenticated ? s : null))
      .catch(() => setSession(null))
      .finally(() => setAuthChecked(true))

    DevLoginEnabled()
      .then(setDevLoginEnabled)
      .catch(() => setDevLoginEnabled(false))

    // PrinterStatus is polled once here in addition to subscribing to the
    // pushed event stream below: hardware:printer events are only emitted
    // on a STATUS TRANSITION (see hardware.Device's doc comment), so a
    // printer that already connected (or failed) before this component
    // finished mounting would otherwise leave the header showing
    // "bekleniyor…" forever.
    PrinterStatus()
      .then((s) => setPrinter({ kind: s.kind, status: s.status as PrinterEvent['status'] }))
      .catch(() => {})

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

  async function handleDevLogin(email: string) {
    try {
      const result = await Login(email)
      setSession(result)
      setAuthError('')
    } catch (err) {
      setAuthError(describeError(err))
    }
  }

  // LoginWithKeycloak blocks (Go-side) until the loopback callback arrives
  // or times out (see app.go's keycloakLoginTimeout) — it either returns a
  // completed session (single membership, auto-selected) or a context list
  // this screen must render a picker for (see ContextPicker/handleSelectContext).
  async function handleKeycloakLogin() {
    setAuthError('')
    setKeycloakLoading(true)
    try {
      const result = await LoginWithKeycloak()
      if (result.needs_context_selection) {
        setPendingContexts(result.contexts ?? [])
      } else {
        setSession(result.session)
        setPendingContexts([])
      }
    } catch (err) {
      setAuthError(describeError(err))
    } finally {
      setKeycloakLoading(false)
    }
  }

  async function handleSelectContext(membershipId: string) {
    setAuthError('')
    setKeycloakLoading(true)
    try {
      const result = await SelectKeycloakContext(membershipId)
      setSession(result)
      setPendingContexts([])
    } catch (err) {
      setAuthError(describeError(err))
    } finally {
      setKeycloakLoading(false)
    }
  }

  async function handleLogout() {
    await Logout()
    setSession(null)
    setPendingContexts([])
    setSelectedCheck(null)
    setConfirmedOrders([])
    setPendingLines([])
    setOpenChecks([])
    setAlreadyPaidTotal(0)
    setZones([])
    setTablesError('')
    setLastReceivedAmount(0)
    setPrintError('')
    setPrintRetryCheckId(null)
  }

  // refreshPaidTotal re-syncs alreadyPaidTotal from the server's own
  // record of completed payments for a check — used both when a check is
  // first selected (see handleSelectCheck below) and after a
  // RegisterCashPayment call fails (see handleRegisterPayment): a failed
  // call might still have landed server-side (network error after the
  // write committed), so the remaining balance shown to the cashier must
  // not silently drift from what the backend actually has on record.
  //
  // IMPORTANT — same permission gap as before: this needs
  // "payment.payment.read", which a plain "cashier" session does not have
  // (see pos.go's ListCheckPayments doc comment). Fail-open on ANY error
  // (403 included) so a permission gap or transient failure never blocks
  // the cashier's flow — it just means alreadyPaidTotal keeps whatever
  // value local accumulation already gave it (0 on first select, or
  // whatever partial payments this session itself already recorded).
  const refreshPaidTotal = useCallback(async (checkId: string) => {
    try {
      const payments = await ListCheckPayments(checkId)
      const paidSoFar = payments.reduce((sum, p) => sum + p.amount_total, 0)
      setAlreadyPaidTotal(paidSoFar)
    } catch (err) {
      console.warn('ListCheckPayments failed — remaining balance may be stale for this check', err)
    }
  }, [])

  async function handleSelectCheck(checkId: string) {
    setReceiptError('')
    setPendingLines([])
    setAlreadyPaidTotal(0)
    // Reset the cumulative "cash physically handed over" tracker for the
    // receipt print (see lastReceivedAmount's doc comment) — it must not
    // carry over from whatever check was selected before this one.
    setLastReceivedAmount(0)
    try {
      const [check, orders] = await Promise.all([GetCheck(checkId), ListCheckOrders(checkId)])
      setSelectedCheck(check)
      setConfirmedOrders(orders)
    } catch (err) {
      setReceiptError(describeError(err))
      return
    }

    // Seed alreadyPaidTotal from any payment already recorded against this
    // check (e.g. the app restarted mid-split, or a manager reopens a
    // check partially paid in an earlier session) — see refreshPaidTotal's
    // doc comment for the permission caveat.
    await refreshPaidTotal(checkId)
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

  // amountToRegister is one cash-payment INSTALLMENT (already clamped to
  // the remaining balance by Receipt.tsx — never the check's full total
  // unless this is the only/final installment); receivedAmount is the raw
  // cash the customer handed over for this installment. Both accumulate
  // rather than overwrite, so a split payment across several calls ends
  // up correct: alreadyPaidTotal reaches confirmedTotal only once every
  // installment has been registered, and lastReceivedAmount reflects the
  // true total cash handed over for the whole check (see its doc comment).
  async function handleRegisterPayment(amountToRegister: number, receivedAmount: number) {
    if (!selectedCheck || !session?.branch_id) return
    setReceiptError('')
    const checkId = selectedCheck.id
    try {
      const payment = await RegisterCashPayment(session.branch_id, checkId, amountToRegister)
      setAlreadyPaidTotal((total) => total + payment.amount_total)
      setLastReceivedAmount((total) => total + receivedAmount)
    } catch (err) {
      setReceiptError(describeError(err))
      // The call may still have landed server-side despite the client-side
      // error (e.g. a network failure after the write committed) — re-sync
      // the remaining balance from the server rather than risk the cashier
      // retrying a payment that already went through (see refreshPaidTotal's
      // doc comment; fail-open, so this is a no-op for a plain cashier
      // session that can't read payments back at all).
      await refreshPaidTotal(checkId)
      throw err
    }
  }

  // printReceiptFor is best-effort by design (task note: "baskı hatası
  // kapanışı ENGELLEMEZ"): a failure here never throws back to its caller —
  // it only records printError/printRetryCheckId so the header can offer
  // "Fişi yeniden yazdır" without the cashier losing the fact that the
  // check itself is already correctly closed/paid.
  async function printReceiptFor(checkId: string, receivedAmount: number) {
    try {
      await PrintReceipt(checkId, receivedAmount)
      setPrintError('')
      setPrintRetryCheckId(null)
    } catch (err) {
      setPrintError(describeError(err))
      setPrintRetryCheckId(checkId)
    }
  }

  async function handleReprintReceipt() {
    if (!printRetryCheckId) return
    await printReceiptFor(printRetryCheckId, lastReceivedAmount)
  }

  async function handleCloseCheck() {
    if (!selectedCheck) return
    setReceiptError('')
    const checkId = selectedCheck.id
    const receivedAmount = lastReceivedAmount
    try {
      await CloseCheck(checkId)
      setSelectedCheck(null)
      setConfirmedOrders([])
      setPendingLines([])
      setAlreadyPaidTotal(0)
      refreshOpenChecks()
      refreshTables(session?.branch_id)
    } catch (err) {
      setReceiptError(describeError(err))
      return
    }
    // Printing happens only after CloseCheck has already succeeded — a
    // print failure must never look like the close itself failed.
    await printReceiptFor(checkId, receivedAmount)
  }

  if (!authChecked) {
    return <div className="flex min-h-screen items-center justify-center bg-surface text-ink-dim">Yükleniyor…</div>
  }

  if (!session?.authenticated) {
    if (pendingContexts.length > 0) {
      return (
        <ContextPicker
          contexts={pendingContexts}
          onSelect={handleSelectContext}
          errorMessage={authError}
          loading={keycloakLoading}
        />
      )
    }
    return (
      <LoginScreen
        onDevLogin={handleDevLogin}
        onKeycloakLogin={handleKeycloakLogin}
        devLoginEnabled={devLoginEnabled}
        errorMessage={authError}
        keycloakLoading={keycloakLoading}
      />
    )
  }

  return (
    <div className="flex h-screen flex-col bg-surface text-ink">
      <header className="flex min-h-12 shrink-0 items-center justify-between border-b border-line px-4 text-sm">
        <span>
          {session.full_name} ({session.email})
        </span>
        <div className="flex items-center gap-4">
          {/*
            Bağlı yazıcı sessizdir — yalnızca kopuk/hata durumunda amber bir
            rozet gösterilir (kırmızı hiçbir zaman: bu app'te kırmızı yalnız
            void/iptal içindir, bkz. style.css). Bu satır o kuralın tek
            istisnasıdır — task-lead'in açık talebiyle amber kullanıldı;
            ui-designer bu rengin "para/ana aksiyon" anlamıyla çakışıp
            çakışmadığını gözden geçirebilir (bkz. rapor).
          */}
          {printer && printer.status !== 'connected' && (
            <span
              className="rounded-full bg-amber/20 px-2 py-0.5 text-xs font-semibold text-ink"
              title={printer.error ?? ''}
            >
              Yazıcı {printer.status === 'error' ? 'hata' : 'bağlı değil'}
            </span>
          )}
          <button type="button" onClick={handleLogout} className="min-h-8 rounded px-2 text-ink-dim">
            Çıkış
          </button>
        </div>
      </header>

      {printError && (
        <div className="flex shrink-0 items-center justify-between gap-3 border-b border-line bg-amber/10 px-4 py-2 text-sm text-ink">
          <span>Fiş yazdırılamadı: {printError}</span>
          <button
            type="button"
            onClick={handleReprintReceipt}
            className="min-h-8 shrink-0 rounded bg-amber px-3 font-semibold text-amber-ink"
          >
            Fişi yeniden yazdır
          </button>
        </div>
      )}

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
