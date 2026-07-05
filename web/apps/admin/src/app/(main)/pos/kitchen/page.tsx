"use client"

import { AlertTriangle, ChefHat, Loader2, Maximize, Minimize, Volume2, VolumeX, Wifi, WifiOff } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Select, SelectItem } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { useAcceptOrder, useAdvanceOrder, useOrder } from "@/hooks/use-pos"
import { useBranches } from "@/hooks/use-tenant"
import { type KitchenConnectionStatus, useKitchenStream } from "@/hooks/use-kitchen-stream"
import { kitchenOrdersByStatus, type KitchenOrder } from "@/lib/kitchen-events"
import { useAuthStore } from "@/store/auth-store"
import type { OrderStatus } from "@/types"

const BRANCH_STORAGE_KEY = "kds-branch-id"
const SOUND_STORAGE_KEY = "kds-sound-enabled"

const COLUMN_ORDER: Extract<OrderStatus, "pending" | "accepted" | "preparing" | "ready">[] = [
  "pending",
  "accepted",
  "preparing",
  "ready",
]

const COLUMN_LABEL: Record<(typeof COLUMN_ORDER)[number], string> = {
  pending: "Bekliyor",
  accepted: "Kabul Edildi",
  preparing: "Hazırlanıyor",
  ready: "Hazır",
}

const COLUMN_ACCENT: Record<(typeof COLUMN_ORDER)[number], string> = {
  pending: "border-t-yellow-500",
  accepted: "border-t-blue-500",
  preparing: "border-t-orange-500",
  ready: "border-t-green-500",
}

// The order status machine only allows one specific next status per state
// (backend/internal/modules/pos/domain/order.go, allowedOrderTransitions) —
// "pending" goes through the separate /accept endpoint, everything else
// through /advance with an explicit target status.
const NEXT_STATUS: Partial<Record<OrderStatus, OrderStatus>> = {
  accepted: "preparing",
  preparing: "ready",
  ready: "delivered",
}

const ACTION_LABEL: Record<(typeof COLUMN_ORDER)[number], string> = {
  pending: "Kabul Et",
  accepted: "Hazırlamaya Başla",
  preparing: "Hazır",
  ready: "Teslim Et",
}

function playBeep() {
  try {
    const AudioContextCtor =
      window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
    const ctx = new AudioContextCtor()
    const oscillator = ctx.createOscillator()
    const gain = ctx.createGain()
    oscillator.type = "sine"
    oscillator.frequency.value = 880
    gain.gain.setValueAtTime(0.15, ctx.currentTime)
    gain.gain.exponentialRampToValueAtTime(0.001, ctx.currentTime + 0.4)
    oscillator.connect(gain)
    gain.connect(ctx.destination)
    oscillator.start()
    oscillator.stop(ctx.currentTime + 0.4)
    oscillator.onended = () => void ctx.close()
  } catch {
    // Web Audio unavailable (older browser, autoplay policy) — sound is an
    // optional nicety, never block the KDS on it.
  }
}

function formatElapsed(occurredAt: string, now: number): string {
  const elapsedSec = Math.max(0, Math.floor((now - new Date(occurredAt).getTime()) / 1000))
  const minutes = Math.floor(elapsedSec / 60)
  const seconds = elapsedSec % 60
  return `${minutes}:${seconds.toString().padStart(2, "0")}`
}

function ConnectionBadge({ status }: { status: KitchenConnectionStatus }) {
  if (status === "live") {
    return (
      <Badge className="border-green-500/40 bg-green-500/15 text-green-400">
        <Wifi className="mr-1 size-3.5" />
        Canlı
      </Badge>
    )
  }
  if (status === "reconnecting") {
    return (
      <Badge className="animate-pulse border-amber-500/40 bg-amber-500/15 text-amber-400">
        <Loader2 className="mr-1 size-3.5 animate-spin" />
        Yeniden bağlanıyor
      </Badge>
    )
  }
  if (status === "error") {
    return (
      <Badge className="border-red-500/40 bg-red-500/15 text-red-400">
        <AlertTriangle className="mr-1 size-3.5" />
        Bağlantı hatası
      </Badge>
    )
  }
  return (
    <Badge className="border-neutral-600 bg-neutral-800 text-neutral-300">
      <WifiOff className="mr-1 size-3.5" />
      Bağlanıyor
    </Badge>
  )
}

function KitchenOrderCard({
  order,
  now,
  isNew,
  onAdvance,
  isMutating,
}: {
  order: KitchenOrder
  now: number
  isNew: boolean
  onAdvance: (order: KitchenOrder) => void
  isMutating: boolean
}) {
  const { data: detail } = useOrder(order.orderId)

  return (
    <Card
      className={`border-t-4 bg-neutral-900 text-neutral-100 ${COLUMN_ACCENT[order.status as (typeof COLUMN_ORDER)[number]]} ${
        isNew ? "ring-4 ring-amber-400 animate-pulse" : ""
      }`}
    >
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <CardTitle className="text-lg">{order.tableLabel || "Masasız"}</CardTitle>
          <span className="font-mono text-sm text-neutral-400">{formatElapsed(order.occurredAt, now)}</span>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {detail ? (
          <ul className="space-y-1">
            {detail.items.map((item) => (
              <li key={item.id} className="flex justify-between text-sm">
                <span>{item.product_name}</span>
                <span className="text-neutral-400">×{item.quantity}</span>
              </li>
            ))}
          </ul>
        ) : (
          <Skeleton className="h-10 w-full bg-neutral-800" />
        )}
        <Button
          size="lg"
          className="h-14 w-full text-base font-semibold"
          onClick={() => onAdvance(order)}
          disabled={isMutating}
        >
          {ACTION_LABEL[order.status as (typeof COLUMN_ORDER)[number]]}
        </Button>
      </CardContent>
    </Card>
  )
}

export default function KitchenPage() {
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""
  const { data: branches, isLoading: branchesLoading } = useBranches(tenantId)
  const [branchId, setBranchId] = useState<string | null>(null)
  const [now, setNow] = useState(() => Date.now())
  const [isFullscreen, setIsFullscreen] = useState(false)
  const [soundEnabled, setSoundEnabled] = useState(false)
  const seenIdsRef = useRef<Set<string>>(new Set())

  const acceptOrder = useAcceptOrder()
  const advanceOrder = useAdvanceOrder()

  useEffect(() => {
    setSoundEnabled(typeof window !== "undefined" && localStorage.getItem(SOUND_STORAGE_KEY) === "true")
  }, [])

  useEffect(() => {
    if (!branches || branches.length === 0) return
    const stored = typeof window !== "undefined" ? localStorage.getItem(BRANCH_STORAGE_KEY) : null
    const initial = stored && branches.some((b) => b.id === stored) ? stored : branches[0].id
    setBranchId((current) => current ?? initial)
  }, [branches])

  useEffect(() => {
    const interval = setInterval(() => setNow(Date.now()), 1_000)
    return () => clearInterval(interval)
  }, [])

  useEffect(() => {
    const handler = () => setIsFullscreen(Boolean(document.fullscreenElement))
    document.addEventListener("fullscreenchange", handler)
    return () => document.removeEventListener("fullscreenchange", handler)
  }, [])

  const { status, orders, newOrderIds, errorMessage } = useKitchenStream(branchId)

  useEffect(() => {
    if (!soundEnabled) return
    for (const id of newOrderIds) {
      if (!seenIdsRef.current.has(id)) {
        seenIdsRef.current.add(id)
        playBeep()
      }
    }
  }, [newOrderIds, soundEnabled])

  const columns = useMemo(() => kitchenOrdersByStatus(orders), [orders])

  const handleBranchChange = (id: string) => {
    setBranchId(id)
    if (typeof window !== "undefined") localStorage.setItem(BRANCH_STORAGE_KEY, id)
  }

  const handleSoundToggle = (checked: boolean) => {
    setSoundEnabled(checked)
    if (typeof window !== "undefined") localStorage.setItem(SOUND_STORAGE_KEY, String(checked))
  }

  const toggleFullscreen = () => {
    if (document.fullscreenElement) {
      void document.exitFullscreen()
    } else {
      void document.documentElement.requestFullscreen().catch(() => {
        toast.error("Tam ekran modu bu tarayıcıda desteklenmiyor")
      })
    }
  }

  const handleAdvance = async (order: KitchenOrder) => {
    try {
      if (order.status === "pending") {
        await acceptOrder.mutateAsync(order.orderId)
        return
      }
      const nextStatus = NEXT_STATUS[order.status]
      if (!nextStatus) return
      await advanceOrder.mutateAsync({ id: order.orderId, status: nextStatus })
    } catch {
      toast.error("İşlem başarısız")
    }
  }

  const isMutating = acceptOrder.isPending || advanceOrder.isPending
  const totalActive = Object.values(columns).reduce((sum, list) => sum + list.length, 0)

  return (
    <div className="dark min-h-screen bg-background p-4 text-foreground md:p-6">
      <div className="mb-6 flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <ChefHat className="size-8 text-primary" />
          <div>
            <h1 className="text-2xl font-bold tracking-tight">Mutfak Ekranı</h1>
            <p className="text-sm text-muted-foreground">{totalActive} aktif sipariş</p>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-3">
          <ConnectionBadge status={status} />

          {branches && branches.length > 1 && (
            <Select
              className="w-48"
              value={branchId ?? ""}
              onValueChange={handleBranchChange}
              aria-label="Şube seçimi"
            >
              {branches.map((branch) => (
                <SelectItem key={branch.id} value={branch.id}>
                  {branch.name}
                </SelectItem>
              ))}
            </Select>
          )}

          <div className="flex items-center gap-2">
            {soundEnabled ? <Volume2 className="size-4" /> : <VolumeX className="size-4" />}
            <Switch checked={soundEnabled} onCheckedChange={handleSoundToggle} aria-label="Bildirim sesi" />
          </div>

          <Button variant="outline" size="icon" onClick={toggleFullscreen} aria-label="Tam ekran">
            {isFullscreen ? <Minimize className="size-4" /> : <Maximize className="size-4" />}
          </Button>
        </div>
      </div>

      {status === "error" && errorMessage && (
        <div className="mb-4 flex items-center gap-2 rounded-md border border-red-500/40 bg-red-500/10 px-4 py-3 text-sm text-red-300">
          <AlertTriangle className="size-4 shrink-0" />
          {errorMessage}
        </div>
      )}

      {branchesLoading || !branchId ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-64 rounded-lg bg-neutral-800" />
          ))}
        </div>
      ) : totalActive === 0 ? (
        <div className="flex flex-col items-center justify-center py-24 text-center">
          <ChefHat className="mb-4 size-12 text-muted-foreground" />
          <h3 className="text-lg font-semibold">Aktif sipariş yok</h3>
          <p className="mt-1 text-sm text-muted-foreground">Yeni sipariş geldiğinde burada görünecek.</p>
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
          {COLUMN_ORDER.map((columnStatus) => (
            <div key={columnStatus} className="space-y-3">
              <div className="flex items-center justify-between px-1">
                <h2 className="text-sm font-semibold text-neutral-300">{COLUMN_LABEL[columnStatus]}</h2>
                <Badge variant="outline" className="border-neutral-700 text-neutral-400">
                  {columns[columnStatus].length}
                </Badge>
              </div>
              <div className="space-y-3">
                {columns[columnStatus].map((order) => (
                  <KitchenOrderCard
                    key={order.orderId}
                    order={order}
                    now={now}
                    isNew={newOrderIds.has(order.orderId)}
                    onAdvance={handleAdvance}
                    isMutating={isMutating}
                  />
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
