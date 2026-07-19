import { useCallback, useEffect, useMemo, useState } from 'react'
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
import { useFiscalStatusPolling, type StatusResolution } from './hooks/useFiscalStatusPolling'
import { useBranchFiscalPending } from './hooks/useBranchFiscalPending'
import {
  checkIdsAwaitingFiscal,
  closeBlockReason as computeCloseBlockReason,
  collectableRemaining,
  describeRemoteFailure,
  isFullyPaid as computeIsFullyPaid,
  parseStatus,
  receivedTotalForPrint,
  remotePendingForCheck,
  remoteSettledForCheck,
  settledTotal,
  unreportedRemoteFailures,
  type TrackedPayment,
} from './lib/fiscalStatus'
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

const EMPTY_SERVER_COMPLETED: ReadonlyMap<string, number> = new Map()

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

  // --- Payment money, split into two buckets (ADR-FISCAL-002) -------------
  //
  // Before the fiscal registration went asynchronous, a single accumulated
  // `alreadyPaidTotal` was correct: RegisterCashPayment returned a COMPLETED
  // payment, so crediting its amount immediately matched what the backend had
  // on record. That is no longer true — POST now returns `pending`, and
  // pos/service.CheckService.Close's paid-in-full guard (TotalPaidForCheck)
  // counts `completed` payments only. Carrying the old single-number model
  // forward would show a check as fully paid the instant the cash was taken,
  // offer "Basılı tutup kapat", and have the backend reject the close.
  //
  //   serverCompleted — id -> amount, from ListCheckPayments (which filters
  //     status='completed' server-side). Keyed by id, not summed, so that a
  //     tracked payment which later appears in this snapshot is not counted
  //     twice. Empty for a cashier-only session (403, fail-open).
  //   trackedPayments — every payment THIS session registered, with its live
  //     fiscal status. Deliberately NOT cleared when the cashier deselects a
  //     check: a pending fiscal record must keep its amber dot on the masa
  //     planı (requirement 5) and keep polling until it settles.
  //
  // See lib/fiscalStatus.ts for the arithmetic these two feed.
  const [serverCompleted, setServerCompleted] = useState<ReadonlyMap<string, number>>(EMPTY_SERVER_COMPLETED)
  const [trackedPayments, setTrackedPayments] = useState<TrackedPayment[]>([])

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

  // Cash handed over for the check that was just closed, captured at close time
  // (trackedPayments is cleared by then) so "Fişi yeniden yazdır" still prints
  // the right ALINAN/para üstü. Derived from the payments whose fiscal record
  // actually settled — see receivedTotalForPrint.
  const [printReceivedAmount, setPrintReceivedAmount] = useState(0)
  const [printError, setPrintError] = useState('')
  const [printRetryCheckId, setPrintRetryCheckId] = useState<string | null>(null)

  // Payment ids of branch-wide fiscal failures the cashier has acknowledged —
  // see visibleRemoteFailures.
  const [dismissedFailureIds, setDismissedFailureIds] = useState<ReadonlySet<string>>(new Set())

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

  // --- Fiscal status polling --------------------------------------------
  //
  // Scoped to the whole app rather than the receipt panel, on purpose: the
  // cashier routinely takes cash and immediately walks back to the masa planı
  // while the ÖKC is still cutting the receipt. Polling only while the payment
  // screen is mounted would freeze that check's amber dot (requirement 5)
  // forever. The hook creates no timer at all once nothing is pending, so the
  // idle cost is zero — and it stops on unmount (logout / app close) as
  // required.
  const handleStatusResolutions = useCallback((resolutions: StatusResolution[]) => {
    setTrackedPayments((prev) =>
      prev.map((p) => {
        const resolved = resolutions.find((r) => r.id === p.id)
        if (!resolved) return p
        return { ...p, status: resolved.status, failureReason: resolved.failureReason }
      }),
    )
  }, [])

  useFiscalStatusPolling(trackedPayments, handleStatusResolutions)

  // --- Branch-wide fiscal visibility --------------------------------------
  //
  // useFiscalStatusPolling above only ever sees payments THIS station
  // registered. That is a real blind spot in a multi-till branch: a colleague
  // takes cash on adisyon #12 at the other register, and this station happily
  // offers "Basılı tutup kapat" on it while the ÖKC is still cutting that
  // receipt — the close then fails server-side (409 fiscal_pending), or worse
  // the same money gets collected twice.
  //
  // The Go poller (fiscal_poller.go) pushes a branch-wide snapshot; everything
  // below merges it with the local tracked list, DEDUPED BY PAYMENT ID (the
  // snapshot contains this station's own payments too — see remotePendingOnly).
  // Identifies WHOSE branch snapshots the state below belongs to. Both the user
  // and the branch matter: a shift change (same station, different cashier) and
  // a context switch (same cashier, different branch) must each invalidate the
  // feed. Without this a logged-out session's failed-fiscal banner and its
  // pending reservations would survive into the next cashier's session.
  const sessionFiscalKey = `${session?.user_id ?? ''}|${session?.branch_id ?? ''}`

  const branchFiscal = useBranchFiscalPending(sessionFiscalKey)

  // Acknowledgements are scoped to the same key: a banner dismissed by the
  // previous cashier must not stay dismissed for the next one, who has not seen
  // it. Cleared here rather than only in handleLogout, which never runs on a
  // branch switch (SelectKeycloakContext) or a restored-session change.
  useEffect(() => {
    setDismissedFailureIds(new Set())
  }, [sessionFiscalKey])

  const selectedCheckId = selectedCheck?.id ?? null

  const trackedForSelected = useMemo(
    () => trackedPayments.filter((p) => p.checkId === selectedCheckId),
    [trackedPayments, selectedCheckId],
  )

  const remoteForSelected = useMemo(
    () => remotePendingForCheck(branchFiscal.pending, selectedCheckId),
    [branchFiscal.pending, selectedCheckId],
  )

  const awaitingFiscalCheckIds = useMemo(
    () => checkIdsAwaitingFiscal(trackedPayments, branchFiscal.pending),
    [trackedPayments, branchFiscal.pending],
  )

  // Failed fiscal registrations reported by the branch feed — including THIS
  // station's own, when the badge is not already showing them as failed. For a
  // cashier session (GetPayment 403s, so own payments sit at `unknown`) this
  // banner is the ONLY channel that ever reports a receipt that was not cut.
  // See unreportedRemoteFailures for the exact suppression rule.
  const remoteFailures = useMemo(
    () => unreportedRemoteFailures(branchFiscal.recentlySettled, trackedPayments),
    [branchFiscal.recentlySettled, trackedPayments],
  )

  // A failure stays in the backend's `recently_settled` window for a while
  // after the cashier has seen and handled it. Without an acknowledgement the
  // banner would sit there for minutes, training the cashier to ignore the one
  // place a real fiscal failure is reported. Dismissal is per payment id and
  // deliberately NOT persisted — a fresh app session should re-surface a still
  // recent failure rather than silently swallow it.
  const visibleRemoteFailures = useMemo(
    () => remoteFailures.filter((f) => !dismissedFailureIds.has(f.paymentId)),
    [remoteFailures, dismissedFailureIds],
  )

  // Settled money from OTHER stations on this check. Fed into all three of
  // settledPaidTotal / remaining / fullyPaid from the same source, on purpose:
  // Receipt.tsx gates "Nakit al" on remaining > 0 and the close button on
  // fullyPaid, so feeding those two from different settled sums would strand
  // the cashier on a check a colleague already paid off (see isFullyPaid).
  const remoteSettledForSelected = useMemo(
    () => remoteSettledForCheck(branchFiscal.recentlySettled, selectedCheckId),
    [branchFiscal.recentlySettled, selectedCheckId],
  )

  const confirmedTotal = confirmedOrdersTotal(confirmedOrders)
  const settledPaidTotal = settledTotal(serverCompleted, trackedForSelected, remoteSettledForSelected)
  const remaining = collectableRemaining(
    confirmedTotal,
    serverCompleted,
    trackedForSelected,
    remoteForSelected,
    remoteSettledForSelected,
  )
  const fullyPaid = computeIsFullyPaid(
    confirmedTotal,
    serverCompleted,
    trackedForSelected,
    remoteSettledForSelected,
  )
  const closeBlockReason = computeCloseBlockReason(trackedForSelected, remoteForSelected)

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
    setServerCompleted(EMPTY_SERVER_COMPLETED)
    setTrackedPayments([])
    setZones([])
    setTablesError('')
    setPrintReceivedAmount(0)
    setPrintError('')
    setPrintRetryCheckId(null)
    setDismissedFailureIds(new Set())
  }

  // refreshServerPayments re-syncs the set of COMPLETED payments the server has
  // on record for a check — used both when a check is first selected and after
  // a RegisterCashPayment call fails (see handleRegisterPayment): a failed call
  // might still have landed server-side (network error after the write
  // committed), so the remaining balance shown to the cashier must not silently
  // drift from what the backend actually has.
  //
  // Keyed by payment id rather than summed: settledTotal() needs the ids to
  // avoid double-counting a payment that appears both here and in
  // trackedPayments (which it will, as soon as it completes).
  //
  // IMPORTANT — permission gap: this needs "payment.payment.read", which a
  // plain "cashier" session does not have (see pos.go's ListCheckPayments doc
  // comment). Fail-open on ANY error (403 included) so a permission gap or
  // transient failure never blocks the cashier's flow.
  const refreshServerPayments = useCallback(async (checkId: string) => {
    try {
      const payments = await ListCheckPayments(checkId)
      setServerCompleted(new Map(payments.map((p) => [p.id, p.amount_total])))
    } catch (err) {
      console.warn('ListCheckPayments failed — remaining balance may be stale for this check', err)
    }
  }, [])

  async function handleSelectCheck(checkId: string) {
    setReceiptError('')
    setPendingLines([])
    setServerCompleted(EMPTY_SERVER_COMPLETED)
    try {
      const [check, orders] = await Promise.all([GetCheck(checkId), ListCheckOrders(checkId)])
      setSelectedCheck(check)
      setConfirmedOrders(orders)
    } catch (err) {
      setReceiptError(describeError(err))
      return
    }

    // Seed from any completed payment already recorded against this check
    // (e.g. the app restarted mid-split, or a manager reopens a check partially
    // paid in an earlier session) — see refreshServerPayments' doc comment for
    // the permission caveat. trackedPayments is NOT reset here: a payment this
    // session registered against this check may still be mid-registration.
    await refreshServerPayments(checkId)
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

  // amountToRegister is one cash-payment INSTALLMENT (already clamped to the
  // remaining balance by Receipt.tsx); receivedAmount is the raw cash the
  // customer handed over for this installment.
  //
  // The returned payment is `pending` (ADR-FISCAL-002): its amount is recorded
  // as tracked-but-unsettled, which reserves it against the remaining balance
  // (so the same money cannot be collected twice) WITHOUT marking the check
  // payable-in-full. The polling hook flips it to completed/failed/voided.
  async function handleRegisterPayment(amountToRegister: number, receivedAmount: number) {
    if (!selectedCheck || !session?.branch_id) return
    setReceiptError('')
    const checkId = selectedCheck.id
    try {
      const payment = await RegisterCashPayment(session.branch_id, checkId, amountToRegister)
      setTrackedPayments((prev) => [
        ...prev,
        {
          id: payment.id,
          checkId,
          amountTotal: payment.amount_total,
          // Trust the server's status verbatim rather than assuming `pending` —
          // a synchronous adapter (or a replayed idempotent POST) may hand back
          // an already-completed payment, which must go straight to green.
          status: parseStatus(payment.status),
          receivedAmount,
          registeredAtMs: Date.now(),
        },
      ])
    } catch (err) {
      setReceiptError(describeError(err))
      // The call may still have landed server-side despite the client-side
      // error (e.g. a network failure after the write committed) — re-sync the
      // remaining balance from the server rather than risk the cashier retrying
      // a payment that already went through (fail-open, so this is a no-op for
      // a plain cashier session that can't read payments back at all).
      await refreshServerPayments(checkId)
      throw err
    }
  }

  // Requirement 3: retry a failed fiscal registration. The failed payment
  // carries no money (it never counted toward settled or reserved — see
  // fiscalStatus.ts), so dropping it from the tracked list simply returns its
  // amount to the collectable balance. The retry itself is just the ordinary
  // cash flow again: Receipt reopens cash mode with the amount prefilled and
  // calls handleRegisterPayment, which POSTs a brand-new payment. The failed
  // one stays on record server-side; nothing here mutates it.
  function handleDiscardFailedPayment(paymentId: string) {
    setTrackedPayments((prev) => prev.filter((p) => p.id !== paymentId))
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
    await printReceiptFor(printRetryCheckId, printReceivedAmount)
  }

  async function handleCloseCheck() {
    if (!selectedCheck) return
    // Requirement 4 — belt and braces. The button is already hidden while a
    // fiscal record is pending (see Receipt), but a close must never slip
    // through: the receipt for a payment still being registered would be
    // printed against a check the backend has not accepted as paid.
    if (closeBlockReason) {
      setReceiptError(`${closeBlockReason} — mali kayıt tamamlanmadan adisyon kapatılamaz.`)
      return
    }
    setReceiptError('')
    const checkId = selectedCheck.id
    const receivedAmount = receivedTotalForPrint(trackedForSelected)
    try {
      await CloseCheck(checkId)
      setSelectedCheck(null)
      setConfirmedOrders([])
      setPendingLines([])
      setServerCompleted(EMPTY_SERVER_COMPLETED)
      setTrackedPayments((prev) => prev.filter((p) => p.checkId !== checkId))
      setPrintReceivedAmount(receivedAmount)
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

      {/*
        Mali kayıt hatası — fişi kesilemeyen bir ödeme (bu istasyonun kendi
        ödemesi de olabilir, bkz. unreportedRemoteFailures). Metin hangi
        istasyon olduğunu İDDİA ETMEZ: şube akışı bunu ayırt etmez, kasiyere
        yanlış yere baktırmaktansa adisyonu söylemek daha yararlıdır.
        Amber, kırmızı değil: bu app'te kırmızı yalnız void/iptal içindir
        (bkz. style.css) ve buradaki ödeme iptal edilmiş değil, yeniden
        alınması gereken bir ödemedir. Ham `failure_reason` cihaz çıktısıdır —
        kasiyere Türkçe mesaj gösterilir, ham metin yalnız title olarak
        taşınır (bkz. describeRemoteFailure).
      */}
      {visibleRemoteFailures.map((failure) => (
        <div
          key={failure.paymentId}
          className="flex shrink-0 items-center justify-between gap-3 border-b border-line bg-amber/10 px-4 py-2 text-sm text-ink"
          title={failure.failureReason ?? ''}
        >
          <span>Mali kayıt hatası: {describeRemoteFailure()}</span>
          <button
            type="button"
            onClick={() =>
              setDismissedFailureIds((prev) => new Set(prev).add(failure.paymentId))
            }
            className="min-h-8 shrink-0 rounded bg-amber px-3 font-semibold text-amber-ink"
          >
            Anladım
          </button>
        </div>
      ))}

      <div className="flex flex-1 overflow-hidden">
        <CheckRail
          checks={openChecks}
          selectedCheckId={selectedCheckId}
          onSelect={handleSelectCheck}
          onOpenTakeaway={handleOpenTakeaway}
          canOpenCheck={canOpenCheck}
          awaitingFiscalCheckIds={awaitingFiscalCheckIds}
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
            awaitingFiscalCheckIds={awaitingFiscalCheckIds}
          />
        )}

        <Receipt
          tableLabel={selectedCheck?.table_label ?? ''}
          confirmedOrders={confirmedOrders}
          pendingLines={pendingLines}
          onRemovePendingLine={handleRemovePendingLine}
          onSendOrder={handleSendOrder}
          sendingOrder={sendingOrder}
          confirmedTotal={confirmedTotal}
          pendingTotal={sumPendingTotal(pendingLines)}
          settledPaidTotal={settledPaidTotal}
          remaining={remaining}
          isFullyPaid={fullyPaid}
          closeBlockReason={closeBlockReason}
          payments={trackedForSelected}
          onRegisterPayment={handleRegisterPayment}
          onDiscardFailedPayment={handleDiscardFailedPayment}
          onCloseCheck={handleCloseCheck}
          errorMessage={receiptError}
        />
      </div>
    </div>
  )
}

export default App
