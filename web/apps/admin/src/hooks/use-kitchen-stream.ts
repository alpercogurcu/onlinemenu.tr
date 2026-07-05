"use client"

import { useEffect, useRef, useState } from "react"

import api, { clearAccessToken, getAccessToken } from "@/lib/api"
import {
  applyKitchenMessage,
  isActiveKitchenStatus,
  isSnapshotMessage,
  type KitchenOrderEvent,
  type KitchenMessage,
  type KitchenOrderMap,
} from "@/lib/kitchen-events"
import type { Check, Order } from "@/types"

export type KitchenConnectionStatus = "connecting" | "live" | "reconnecting" | "error"

const BASE_BACKOFF_MS = 1_000
const MAX_BACKOFF_MS = 30_000
const NEW_ORDER_HIGHLIGHT_MS = 4_000

// How long a new-order highlight (visual glow) is applied before it clears,
// mirroring what the KDS board renders it for.
export { NEW_ORDER_HIGHLIGHT_MS }

interface UseKitchenStreamResult {
  status: KitchenConnectionStatus
  orders: KitchenOrderMap
  newOrderIds: ReadonlySet<string>
  errorMessage: string | null
}

// hydrateReadyOrders backfills "ready" orders after every snapshot.
//
// backend/internal/modules/pos/repo/order_repo.go's ListActiveByBranch (the
// query behind the WS snapshot) deliberately scopes "live for the kitchen"
// to pending/accepted/preparing — "ready" orders are NOT included. Live
// order.status_changed events into/out of "ready" still arrive normally, so
// this only matters on (re)connect: a fresh snapshot clears the board and
// would silently drop any order that was already sitting in "ready",
// because nothing ever re-announces it. Reconnects are routine here (every
// network blip triggers one via the backoff loop below), so without this
// backfill a kitchen's "ready, waiting for pickup" orders would vanish from
// the screen on any hiccup. Backend is out of scope for this change
// (KAPSAM: web/apps/admin/**), so this closes the gap client-side using the
// existing checks/orders REST endpoints — best-effort: a failure here just
// means the next live event (if any) is what restores that one order.
async function hydrateReadyOrders(branchId: string): Promise<KitchenOrderEvent[]> {
  const { data: checks } = await api.get<Check[]>("/api/v1/pos/checks")
  const branchChecks = (checks ?? []).filter((c) => c.branch_id === branchId && c.status === "open")

  const perCheck = await Promise.all(
    branchChecks.map(async (check): Promise<KitchenOrderEvent[]> => {
      try {
        const { data: orders } = await api.get<Order[]>(`/api/v1/pos/checks/${check.id}/orders`)
        return (orders ?? [])
          .filter((o) => o.status === "ready")
          .map((o) => ({
            type: "order.status_changed" as const,
            order_id: o.id,
            check_id: o.check_id,
            table_label: check.table_label,
            status: o.status,
            // seq: 0 mirrors the snapshot convention (kitchen-events.ts
            // treats seq 0 as "always apply, this is a state re-sync, not a
            // sequenced event") so a concurrent live event for the same
            // order (which carries a real seq) is never clobbered by this
            // best-effort backfill.
            seq: 0,
            occurred_at: o.updated_at,
          }))
      } catch {
        return []
      }
    }),
  )
  return perCheck.flat()
}

// useKitchenStream consumes the /api/pos/kitchen-stream bridge route (see
// that route's header comment for why a bridge exists at all) and maintains
// KDS board state: an order_id-keyed map merged via seq last-writer-wins,
// plus a reconnect state machine with exponential backoff + jitter. A fresh
// snapshot (sent by the backend immediately on every (re)connect) fully
// replaces prior state, so a dropped connection self-heals with no gap
// (modulo the "ready" backfill above).
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
    const highlightTimers = new Set<ReturnType<typeof setTimeout>>()
    // Guards against a narrow race: hydrateReadyOrders() is an async REST
    // round-trip running concurrently with the live stream. If an order
    // reaches a terminal status (e.g. ready -> delivered) via a live event
    // *while that fetch is in flight*, the fetch's snapshot-in-time view can
    // still say "ready" and would otherwise resurrect a phantom card with no
    // future event to ever clear it again (unlike the backend's own
    // acknowledged snapshot/broadcast race, which self-heals on the next
    // status change). Any order_id seen leaving the active set is recorded
    // here and the hydration result for it is dropped instead of applied.
    const terminalIds = new Set<string>()
    // Guards a second, rarer instance of the same class of race: a
    // hydration fetch from snapshot N is still in flight when a *second*
    // reconnect (snapshot N+1) starts a newer hydration; if fetch N resolves
    // after fetch N+1, it would apply stale "ready" data on top of the
    // current, more-recent state. Each hydration captures the current
    // generation before awaiting and discards its result if a newer
    // snapshot has since arrived.
    let hydrationGeneration = 0

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

    const scheduleReconnect = () => {
      if (cancelled) return
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
      if (!isSnapshotMessage(msg) && !isActiveKitchenStatus(msg.status)) {
        terminalIds.add(msg.order_id)
      }
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
      abortController = new AbortController()
      const token = getAccessToken()

      let res: Response
      try {
        res = await fetch(`/api/pos/kitchen-stream?branch_id=${encodeURIComponent(branchId)}`, {
          headers: token ? { Authorization: `Bearer ${token}` } : {},
          signal: abortController.signal,
          cache: "no-store",
        })
      } catch (err) {
        if (cancelled || (err instanceof DOMException && err.name === "AbortError")) return
        scheduleReconnect()
        return
      }

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
      if (!res.ok || !res.body) {
        scheduleReconnect()
        return
      }

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
            attempt = 0
            setStatus("live")

            if (msg.type === "snapshot") {
              terminalIds.clear()
              const generation = ++hydrationGeneration
              hydrateReadyOrders(branchId)
                .then((readyEvents) => {
                  if (cancelled || generation !== hydrationGeneration) return
                  for (const evt of readyEvents) {
                    if (terminalIds.has(evt.order_id)) continue
                    applyAndBroadcast(evt)
                  }
                })
                .catch(() => {
                  // Best-effort — see hydrateReadyOrders's doc comment.
                })
            }
          }
        }
      } catch (err) {
        if (cancelled || (err instanceof DOMException && err.name === "AbortError")) return
      }

      if (!cancelled) scheduleReconnect()
    }

    void connect()

    return () => {
      cancelled = true
      abortController?.abort()
      if (retryTimer) clearTimeout(retryTimer)
      for (const timer of highlightTimers) clearTimeout(timer)
    }
  }, [branchId])

  return { status, orders, newOrderIds, errorMessage }
}
