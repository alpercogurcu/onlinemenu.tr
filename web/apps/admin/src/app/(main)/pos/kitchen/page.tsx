"use client"

import {
  AlertTriangle,
  ChefHat,
  Loader2,
  Maximize,
  Minimize,
  Moon,
  QrCode,
  Volume2,
  VolumeX,
  Wifi,
  WifiOff,
} from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Select, SelectItem } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { useAcceptOrder, useAdvanceOrder, useOrderDetails } from "@/hooks/use-pos"
import { useBranches } from "@/hooks/use-tenant"
import { type KitchenConnectionStatus, useKitchenStream } from "@/hooks/use-kitchen-stream"
import { ELAPSED_TONE_CLASS, elapsedTone, formatElapsed } from "@/lib/kds-format"
import { kitchenOrdersByStatus, type KitchenOrder } from "@/lib/kitchen-events"
import { useDeviceDark } from "@/lib/use-device-dark"
import { cn } from "@/lib/utils"
import { useAuthStore } from "@/store/auth-store"
import type { Order, OrderStatus } from "@/types"

const BRANCH_STORAGE_KEY = "kds-branch-id"
const SOUND_STORAGE_KEY = "kds-sound-enabled"
const DARK_STORAGE_KEY = "kds-dark"

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

// Column accents read the semantic status tokens so the board follows the
// app theme (and the per-device dark toggle) instead of painting its own.
const COLUMN_ACCENT: Record<(typeof COLUMN_ORDER)[number], string> = {
  pending: "border-t-status-warning-fg",
  accepted: "border-t-status-info-fg",
  preparing: "border-t-status-info-fg",
  ready: "border-t-status-success-fg",
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

function ConnectionBadge({ status }: { status: KitchenConnectionStatus }) {
  if (status === "live") {
    return (
      <Badge variant="success">
        <Wifi className="mr-1 size-3.5" />
        Canlı
      </Badge>
    )
  }
  if (status === "syncing") {
    return (
      <Badge variant="info">
        <Loader2 className="mr-1 size-3.5 animate-spin" />
        Senkronize ediliyor
      </Badge>
    )
  }
  if (status === "reconnecting") {
    return (
      <Badge variant="warning" className="animate-pulse">
        <Loader2 className="mr-1 size-3.5 animate-spin" />
        Yeniden bağlanıyor
      </Badge>
    )
  }
  if (status === "error") {
    return (
      <Badge variant="danger">
        <AlertTriangle className="mr-1 size-3.5" />
        Bağlantı hatası
      </Badge>
    )
  }
  return (
    <Badge variant="neutral">
      <WifiOff className="mr-1 size-3.5" />
      Bağlanıyor
    </Badge>
  )
}

function KitchenOrderCard({
  order,
  detail,
  now,
  isNew,
  onAdvance,
  isMutating,
}: {
  order: KitchenOrder
  detail?: Order
  now: number
  isNew: boolean
  onAdvance: (order: KitchenOrder) => void
  isMutating: boolean
}) {
  return (
    <Card
      className={cn(
        "border-t-4 bg-card text-card-foreground",
        COLUMN_ACCENT[order.status as (typeof COLUMN_ORDER)[number]],
        isNew && "animate-pulse ring-4 ring-status-warning-border",
      )}
    >
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between gap-2">
          <CardTitle className="flex min-w-0 items-center gap-2 text-lg">
            <span className="truncate">{order.tableLabel || "Masasız"}</span>
            {/* Only "online_qr" is badged. A missing source (older backend,
                omitempty on the wire) and the "pos" default both render
                nothing — the absence of a badge is what means "staff-placed",
                so an unknown value is never mislabelled as one or the other. */}
            {order.source === "online_qr" && (
              <Badge
                variant="outline"
                className="shrink-0 border-status-info-border bg-status-info-bg text-status-info-fg"
                title="QR ile müşteri siparişi"
              >
                <QrCode className="mr-1 size-3" />
                QR
              </Badge>
            )}
          </CardTitle>
          <span
            className={cn("shrink-0 font-mono text-sm tabular-nums", ELAPSED_TONE_CLASS[elapsedTone(order.occurredAt, now)])}
          >
            {formatElapsed(order.occurredAt, now)}
          </span>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {detail ? (
          <ul className="space-y-1">
            {detail.items.map((item) => (
              <li key={item.id} className="flex justify-between text-sm">
                <span>{item.product_name}</span>
                <span className="text-muted-foreground">×{item.quantity}</span>
              </li>
            ))}
          </ul>
        ) : (
          <Skeleton className="h-10 w-full" />
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
  const [deviceDark, setDeviceDark] = useDeviceDark(DARK_STORAGE_KEY)
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

  const allOrderIds = useMemo(
    () => COLUMN_ORDER.flatMap((columnStatus) => columns[columnStatus].map((order) => order.orderId)),
    [columns],
  )
  const details = useOrderDetails(allOrderIds)

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
    <div
      data-kds-root
      data-theme={deviceDark ? "dark" : undefined}
      className={cn(deviceDark && "dark", "min-h-screen bg-background p-4 text-foreground md:p-6")}
    >
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

          <div className="flex items-center gap-2">
            <Moon className="size-4" />
            <Switch checked={deviceDark} onCheckedChange={setDeviceDark} aria-label="Bu cihazda koyu mod" />
          </div>

          <Button variant="outline" size="icon" onClick={toggleFullscreen} aria-label="Tam ekran">
            {isFullscreen ? <Minimize className="size-4" /> : <Maximize className="size-4" />}
          </Button>
        </div>
      </div>

      {/* A transient failure now carries a message too (timeout / dropped
          stream), so the banner is rendered for "reconnecting" as well —
          amber rather than red, because that state recovers on its own. */}
      {errorMessage && (status === "error" || status === "reconnecting") && (
        <div
          role="status"
          className={
            status === "error"
              ? "mb-4 flex items-center gap-2 rounded-md border border-status-danger-border bg-status-danger-bg px-4 py-3 text-sm text-status-danger-fg"
              : "mb-4 flex items-center gap-2 rounded-md border border-status-warning-border bg-status-warning-bg px-4 py-3 text-sm text-status-warning-fg"
          }
        >
          <AlertTriangle className="size-4 shrink-0" />
          {errorMessage}
        </div>
      )}

      {branchesLoading || !branchId ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-64 rounded-lg" />
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
                <h2 className="text-sm font-semibold text-muted-foreground">{COLUMN_LABEL[columnStatus]}</h2>
                <Badge variant="neutral">
                  {columns[columnStatus].length}
                </Badge>
              </div>
              <div className="space-y-3">
                {columns[columnStatus].map((order) => (
                  <KitchenOrderCard
                    key={order.orderId}
                    order={order}
                    detail={details.get(order.orderId)}
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
