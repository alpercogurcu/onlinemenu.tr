"use client"

import { useEffect, useRef, useState } from "react"

import { clearAccessToken, getAccessToken } from "@/lib/api"
import { applyKitchenMessage, type KitchenMessage, type KitchenOrderMap } from "@/lib/kitchen-events"

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
