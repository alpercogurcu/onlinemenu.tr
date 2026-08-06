"use client"

import { useEffect, useRef, useState } from "react"

import { clearAccessToken, getAccessToken } from "@/lib/api"
import { applyKitchenMessage, type KitchenMessage, type KitchenOrderMap } from "@/lib/kitchen-events"

// Connection phases the KDS surfaces, in the order a healthy connect walks
// through them:
//   connecting   — request in flight, no response headers yet
//   syncing      — stream is open, waiting for the backend's initial snapshot
//   live         — snapshot received, board is current
//   reconnecting — backing off before the next attempt (transient failure)
//   error        — permanently stopped, a retry cannot fix it (401/403/422)
//
// "syncing" exists because the two halves used to be indistinguishable: the
// status only flipped to "live" on the first NDJSON line, so a slow snapshot
// (the 16s N+1 that this work fixed backend-side) rendered as an
// indefinite "Bağlanıyor" with no way to tell a stalled socket from a slow
// query.
export type KitchenConnectionStatus = "connecting" | "syncing" | "live" | "reconnecting" | "error"

const BASE_BACKOFF_MS = 1_000
const MAX_BACKOFF_MS = 30_000
const NEW_ORDER_HIGHLIGHT_MS = 4_000

// How long one attempt may spend between "request sent" and "first byte of
// the snapshot" before it is treated as stalled, aborted and retried. Kept
// well above the healthy snapshot cost (~0.1s for a branch with ~740 live
// orders after the backend batch fix) so an ordinary busy branch never trips
// it, and well below the point where a kiosk screen looks hung.
const CONNECT_TIMEOUT_MS = 15_000

const TIMEOUT_MESSAGE = "Sunucu yanıt vermedi, yeniden deneniyor…"
const TRANSIENT_MESSAGE = "Bağlantı koptu, yeniden deneniyor…"

// How long a new-order highlight (visual glow) is applied before it clears,
// mirroring what the KDS board renders it for.
export { NEW_ORDER_HIGHLIGHT_MS }

interface UseKitchenStreamResult {
  status: KitchenConnectionStatus
  orders: KitchenOrderMap
  newOrderIds: ReadonlySet<string>
  errorMessage: string | null
}

// useKitchenStream consumes the /api/pos/kitchen-stream bridge route (see
// that route's header comment for why a bridge exists at all) and maintains
// KDS board state: an order_id-keyed map merged via seq last-writer-wins,
// plus a reconnect state machine with exponential backoff + jitter. A fresh
// snapshot (sent by the backend immediately on every (re)connect) fully
// replaces prior state, so a dropped connection self-heals with no gap.
//
// Note: this hook used to backfill "ready" orders client-side after every
// snapshot, because backend/internal/modules/pos/repo/order_repo.go's
// ListActiveByBranch (the query behind the WS snapshot) excluded "ready"
// from what it considered "live for the kitchen". That backend gap is now
// fixed (domain.KitchenActiveOrderStatuses includes "ready"), so the
// snapshot itself carries ready orders and the client-side compensation
// was removed — see git history for the old hydrateReadyOrders.
export function useKitchenStream(branchId: string | null): UseKitchenStreamResult {
  const [status, setStatus] = useState<KitchenConnectionStatus>("connecting")
  const [orders, setOrders] = useState<KitchenOrderMap>(() => new Map())
  const [newOrderIds, setNewOrderIds] = useState<ReadonlySet<string>>(new Set())
  const [errorMessage, setErrorMessage] = useState<string | null>(null)
  const mapRef = useRef<KitchenOrderMap>(new Map())

  useEffect(() => {
    if (!branchId) return

    mapRef.current = new Map()
    setOrders(new Map())
    setNewOrderIds(new Set())
    setErrorMessage(null)
    setStatus("connecting")

    let cancelled = false
    let attempt = 0
    let abortController: AbortController | null = null
    let retryTimer: ReturnType<typeof setTimeout> | null = null
    let stallTimer: ReturnType<typeof setTimeout> | null = null
    const highlightTimers = new Set<ReturnType<typeof setTimeout>>()

    const clearNewOrderHighlight = (orderId: string) => {
      const timer = setTimeout(() => {
        highlightTimers.delete(timer)
        if (cancelled) return
        setNewOrderIds((prev) => {
          if (!prev.has(orderId)) return prev
          const next = new Set(prev)
          next.delete(orderId)
          return next
        })
      }, NEW_ORDER_HIGHLIGHT_MS)
      highlightTimers.add(timer)
    }

    const scheduleReconnect = (message: string) => {
      if (cancelled) return
      setErrorMessage(message)
      setStatus("reconnecting")
      const backoff = Math.min(BASE_BACKOFF_MS * 2 ** attempt, MAX_BACKOFF_MS)
      attempt += 1
      const jitter = backoff * 0.3 * Math.random()
      retryTimer = setTimeout(() => {
        void connect()
      }, backoff + jitter)
    }

    // failPermanently stops the reconnect loop for errors that a retry can
    // never fix on its own (bad branch, no permission on this branch). This
    // is the fix for a real gap: without it, a 401/403/422 funnelled into
    // scheduleReconnect() and looped "Yeniden bağlanıyor" forever on a kiosk
    // screen that nobody is watching to notice the real problem.
    const failPermanently = (message: string) => {
      setErrorMessage(message)
      setStatus("error")
    }

    const applyAndBroadcast = (msg: KitchenMessage) => {
      const newOrderId = applyKitchenMessage(mapRef.current, msg)
      setOrders(new Map(mapRef.current))
      if (newOrderId) {
        setNewOrderIds((prev) => new Set(prev).add(newOrderId))
        clearNewOrderHighlight(newOrderId)
      }
      return newOrderId
    }

    const connect = async () => {
      if (cancelled) return
      setStatus("connecting")
      abortController = new AbortController()
      const controller = abortController
      const token = getAccessToken()

      // The stall guard covers the whole "request sent → first snapshot line"
      // window, not just the fetch: an NDJSON stream whose headers arrive but
      // whose body never produces a line (a hung backend read, a proxy that
      // opened the response early) is exactly the failure this exists for,
      // and awaiting fetch() alone would never see it.
      let timedOut = false
      let sawFirstMessage = false
      stallTimer = setTimeout(() => {
        if (cancelled || sawFirstMessage) return
        timedOut = true
        controller.abort()
      }, CONNECT_TIMEOUT_MS)
      const clearStallTimer = () => {
        if (stallTimer) clearTimeout(stallTimer)
        stallTimer = null
      }

      // Both "we tore this connection down on purpose" (unmount/branch
      // change → stop) and "our own stall guard fired" (→ retry) surface as
      // the same AbortError, so `timedOut` — not the error — is what decides
      // between them; this helper only recognises the shape.
      const isAbortError = (err: unknown) => err instanceof DOMException && err.name === "AbortError"

      let res: Response
      try {
        res = await fetch(`/api/pos/kitchen-stream?branch_id=${encodeURIComponent(branchId)}`, {
          headers: token ? { Authorization: `Bearer ${token}` } : {},
          signal: controller.signal,
          cache: "no-store",
        })
      } catch (err) {
        clearStallTimer()
        if (cancelled) return
        if (timedOut) {
          scheduleReconnect(TIMEOUT_MESSAGE)
          return
        }
        if (isAbortError(err)) return
        scheduleReconnect(TRANSIENT_MESSAGE)
        return
      }

      // No usable stream: this attempt ends here, so the guard is disarmed up
      // front — leaving it armed would abort whatever attempt happens to be
      // running when it fires.
      if (!res.ok || !res.body) {
        clearStallTimer()

        if (res.status === 401) {
          // Mirrors src/lib/api.ts's axios 401 interceptor: this fetch bypasses
          // that interceptor entirely (it isn't routed through the axios
          // instance), so an expired session on an all-day KDS kiosk screen
          // would otherwise 401 forever on every reconnect with no visible
          // explanation and no path back to /login.
          clearAccessToken()
          if (typeof window !== "undefined") window.location.href = "/login"
          failPermanently("Oturum süresi doldu, giriş sayfasına yönlendiriliyor…")
          return
        }
        if (res.status === 403) {
          failPermanently("Bu şube için mutfak ekranı yetkiniz yok.")
          return
        }
        if (res.status === 422) {
          failPermanently("Geçersiz şube seçimi.")
          return
        }
        scheduleReconnect(TRANSIENT_MESSAGE)
        return
      }

      // Headers are in and the body is open, but the snapshot has not landed
      // yet — this is the phase that used to be indistinguishable from
      // "connecting".
      setStatus("syncing")

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ""

      try {
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split("\n")
          buffer = lines.pop() ?? ""

          for (const line of lines) {
            if (!line) continue
            let msg: KitchenMessage
            try {
              msg = JSON.parse(line) as KitchenMessage
            } catch {
              continue
            }
            applyAndBroadcast(msg)
            if (!sawFirstMessage) {
              sawFirstMessage = true
              clearStallTimer()
            }
            attempt = 0
            setErrorMessage(null)
            setStatus("live")
          }
        }
      } catch (err) {
        clearStallTimer()
        if (cancelled) return
        if (timedOut) {
          scheduleReconnect(TIMEOUT_MESSAGE)
          return
        }
        if (isAbortError(err)) return
        scheduleReconnect(TRANSIENT_MESSAGE)
        return
      }

      clearStallTimer()
      if (!cancelled) scheduleReconnect(TRANSIENT_MESSAGE)
    }

    void connect()

    return () => {
      cancelled = true
      abortController?.abort()
      if (retryTimer) clearTimeout(retryTimer)
      if (stallTimer) clearTimeout(stallTimer)
      for (const timer of highlightTimers) clearTimeout(timer)
    }
  }, [branchId])

  return { status, orders, newOrderIds, errorMessage }
}
