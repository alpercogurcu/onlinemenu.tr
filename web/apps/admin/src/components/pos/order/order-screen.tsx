"use client"

import { ArrowLeft, CircleCheckBig, Sparkles } from "lucide-react"
import { useTranslations } from "next-intl"
import { useEffect, useRef, useState } from "react"
import { toast } from "sonner"

import {
  addProductToPending,
  changePendingQuantity,
  formatMoney,
  pendingTotal,
  removePendingLine,
  toOrderItemInputs,
  type LineOptions,
  type ModifierGroupSource,
  type PendingLine,
} from "@onlinemenu/pos-core"

import { CartBar, SendButton, SendError } from "@/components/pos/order/cart-bar"
import { CartLines } from "@/components/pos/order/cart-lines"
import { OptionPanel } from "@/components/pos/order/option-panel"
import { CategoryChips, ProductGrid } from "@/components/pos/order/product-grid"
import { SentItems } from "@/components/pos/order/sent-items"
import { TouchConfirm, TouchSheet } from "@/components/pos/order/touch-sheet"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { useTables } from "@/hooks/use-pos"
import {
  useBranchProducts,
  useCleanTable,
  useOpenTableCheck,
  useOrderCategories,
  usePlaceTableOrder,
  useProductOptions,
  useRefreshOrderCatalog,
} from "@/hooks/use-pos-order"
import {
  describeOrderError,
  newIdempotencyKey,
  orderSignature,
  placeOrderBody,
  submissionKeyFor,
  tapHaptic,
  type OrderError,
  type SubmissionKey,
} from "@/lib/pos-order"
import { tableStatusVariant } from "@/lib/status-badge"
import type { Product } from "@/types"

// Products without a category still have to be sellable; they get their own
// chip at the end instead of disappearing from the screen.
const UNCATEGORISED = "__none__"

interface OrderScreenProps {
  branchId: string
  tableId: string
  onBackToTables: () => void
}

interface SuccessState {
  count: number
  total: number
  joined: boolean
}

/**
 * The waiter's order screen for one table (docs/pos-ux-spec.md §2-§3a):
 * categories → big product tiles → cart → "Mutfağa gönder".
 *
 * The check (adisyon) is opened lazily, on the first send, not when the table
 * is tapped: a waiter may open, change and cancel a check only through a
 * cashier (no pos.check.cancel), so a mis-tap on an empty table must not leave
 * an occupied table behind. To the waiter the flow looks the same.
 */
export function OrderScreen({ branchId, tableId, onBackToTables }: OrderScreenProps) {
  const t = useTranslations("posOrder")
  const { data: plan, isLoading: planLoading, refetch: refetchPlan } = useTables(branchId, {
    refetchInterval: 20_000,
  })
  const categoriesQuery = useOrderCategories()
  const productsQuery = useBranchProducts(branchId)
  const refreshCatalog = useRefreshOrderCatalog()
  const openCheck = useOpenTableCheck()
  const placeOrder = usePlaceTableOrder()
  const clean = useCleanTable()

  const [openedCheckId, setOpenedCheckId] = useState<string | null>(null)
  const [activeCategory, setActiveCategory] = useState<string | null>(null)
  const [lines, setLines] = useState<PendingLine[]>([])
  const [picker, setPicker] = useState<{ product: Product; groups: ModifierGroupSource[] } | null>(null)
  const [resolvingId, setResolvingId] = useState<string | null>(null)
  const [flashId, setFlashId] = useState<string | null>(null)
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<OrderError | null>(null)
  const [success, setSuccess] = useState<SuccessState | null>(null)
  const [cartOpen, setCartOpen] = useState(false)
  const [leaveOpen, setLeaveOpen] = useState(false)
  const submission = useRef<SubmissionKey | null>(null)
  const flashTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => () => {
    if (flashTimer.current) clearTimeout(flashTimer.current)
  }, [])

  const table = plan?.flatMap((zone) => zone.tables).find((tb) => tb.id === tableId)
  const checkId = openedCheckId ?? table?.active_check_id ?? null

  const products = productsQuery.data ?? []
  const byCategory = new Map<string, Product[]>()
  for (const product of products) {
    const key = product.category_id ?? UNCATEGORISED
    byCategory.set(key, [...(byCategory.get(key) ?? []), product])
  }
  const categories = [
    ...(categoriesQuery.data ?? []).filter((c) => byCategory.has(c.id)),
    ...(byCategory.has(UNCATEGORISED)
      ? [{ id: UNCATEGORISED, name: t("otherCategory"), description: "", sort_order: 0, is_active: true, tenant_id: "", created_at: "", updated_at: "" }]
      : []),
  ]
  const categoryId = categories.some((c) => c.id === activeCategory) ? activeCategory! : (categories[0]?.id ?? "")
  const visible = byCategory.get(categoryId) ?? []
  const { optionsFor, resolve } = useProductOptions(branchId)

  const total = pendingTotal(lines)
  const quantities = new Map<string, number>()
  for (const line of lines) quantities.set(line.productId, (quantities.get(line.productId) ?? 0) + line.quantity)

  function editLines(next: (current: PendingLine[]) => PendingLine[]) {
    setLines(next)
    setError(null)
  }

  function addToCart(product: Product, options?: LineOptions, optionsUnavailable = false) {
    editLines((current) => addProductToPending(current, { ...product, options_unavailable: optionsUnavailable }, options))
    tapHaptic()
    setFlashId(product.id)
    if (flashTimer.current) clearTimeout(flashTimer.current)
    flashTimer.current = setTimeout(() => setFlashId(null), 400)
  }

  async function handleTap(product: Product) {
    if (resolvingId) return
    let lookup = optionsFor(product.id)
    if (lookup === undefined) {
      setResolvingId(product.id)
      try {
        lookup = await resolve(product.id)
      } catch {
        // Selling never waits on the catalog (spec §3a hata halleri): add the
        // product plain and let the line tell the waiter to say it aloud.
        addToCart(product, undefined, true)
        return
      } finally {
        setResolvingId(null)
      }
    }
    if (lookup.kind === "unavailable") addToCart(product, undefined, true)
    else if (lookup.groups.length > 0) setPicker({ product, groups: lookup.groups })
    else addToCart(product)
  }

  async function ensureCheck(): Promise<{ id: string; joined: boolean }> {
    if (checkId) return { id: checkId, joined: false }
    if (!table) throw new Error("table not loaded")
    try {
      const check = await openCheck.mutateAsync({ branch_id: branchId, table_id: table.id, table_label: table.name })
      setOpenedCheckId(check.id)
      return { id: check.id, joined: false }
    } catch (err) {
      // Someone else opened this table a moment ago: it is the same table, so
      // the round belongs on that check — pick it up instead of failing.
      if (describeOrderError(err).kind !== "occupied") throw err
      const fresh = (await refetchPlan()).data?.flatMap((z) => z.tables).find((tb) => tb.id === tableId)
      if (!fresh?.active_check_id) throw err
      setOpenedCheckId(fresh.active_check_id)
      return { id: fresh.active_check_id, joined: true }
    }
  }

  async function send() {
    if (lines.length === 0 || sending) return
    if (table?.status === "cleaning" && !checkId) {
      setError({ kind: "other", code: null, message: t("cleaningBanner") })
      return
    }
    setSending(true)
    setError(null)
    try {
      const check = await ensureCheck()
      const items = toOrderItemInputs(lines)
      submission.current = submissionKeyFor(submission.current, orderSignature(check.id, items), newIdempotencyKey)
      await placeOrder.mutateAsync({
        body: placeOrderBody(branchId, check.id, items),
        idempotencyKey: submission.current.key,
      })
      submission.current = null
      setSuccess({ count: lines.length, total, joined: check.joined })
      setLines([])
      setCartOpen(false)
      tapHaptic(30)
    } catch (err) {
      const described = describeOrderError(err)
      setError(described)
      if (described.kind === "price") void refreshCatalog()
      if (described.kind === "not_open") {
        // The check was closed/cancelled at the counter meanwhile: the next
        // send opens a fresh one for this table.
        setOpenedCheckId(null)
        submission.current = null
        void refetchPlan()
      }
    } finally {
      setSending(false)
    }
  }

  function requestLeave() {
    if (lines.length > 0) setLeaveOpen(true)
    else onBackToTables()
  }

  async function markClean() {
    if (!table) return
    try {
      await clean.mutateAsync(table.id)
      await refetchPlan()
      setError(null)
    } catch (err) {
      toast.error(describeOrderError(err).message || t("cleaningFailed"))
    }
  }

  if (planLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-12 w-full rounded-xl" />
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-24 rounded-2xl" />
          ))}
        </div>
      </div>
    )
  }

  if (!table) {
    return (
      <div className="space-y-4 py-12 text-center">
        <p className="text-base text-muted-foreground">{t("tableNotFound")}</p>
        <button
          type="button"
          onClick={onBackToTables}
          className="bg-primary text-primary-foreground min-h-14 rounded-xl px-6 text-base font-bold"
        >
          {t("backToTables")}
        </button>
      </div>
    )
  }

  const catalogFailed = productsQuery.isError || categoriesQuery.isError
  const catalogLoading = productsQuery.isLoading || categoriesQuery.isLoading
  const showCleaning = table.status === "cleaning" && !checkId

  return (
    <div className="lg:grid lg:grid-cols-[minmax(0,1fr)_380px] lg:items-start lg:gap-6">
      {/* Phone/tablet: fill the viewport below the app header (4rem) and the
          page padding (2rem) so the sticky cart bar sits at the bottom even
          when a category has only a few products. */}
      <div className="flex min-w-0 flex-col gap-4 max-lg:min-h-[calc(100dvh-6rem)]">
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={requestLeave}
            className="bg-card flex min-h-12 shrink-0 items-center gap-2 rounded-xl border-2 px-3 text-base font-semibold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
          >
            <ArrowLeft className="size-5" aria-hidden="true" />
            {t("backToTables")}
          </button>
          <h1 className="min-w-0 flex-1 truncate text-2xl font-bold tracking-tight">{table.name}</h1>
          <Badge variant={tableStatusVariant(table.status)} className="shrink-0 px-2.5 py-1 text-sm">
            {t(`status.${table.status}`)}
          </Badge>
        </div>

        {showCleaning && (
          <div className="border-status-warning-border bg-status-warning-bg text-status-warning-fg flex flex-wrap items-center gap-3 rounded-xl border px-4 py-3">
            <p className="min-w-0 flex-1 text-base font-medium">{t("cleaningBanner")}</p>
            <button
              type="button"
              onClick={() => void markClean()}
              disabled={clean.isPending}
              className="flex min-h-12 items-center gap-2 rounded-lg border bg-background px-4 text-base font-semibold text-foreground disabled:opacity-50"
            >
              <Sparkles className="size-4" aria-hidden="true" />
              {t("markClean")}
            </button>
          </div>
        )}

        {checkId && <SentItems checkId={checkId} />}

        {catalogFailed ? (
          <div className="space-y-3 py-8 text-center">
            <p className="text-base text-muted-foreground">{t("catalogFailed")}</p>
            <button
              type="button"
              onClick={() => void refreshCatalog()}
              className="min-h-12 rounded-xl border-2 px-5 text-base font-semibold"
            >
              {t("retry")}
            </button>
          </div>
        ) : catalogLoading ? (
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3" aria-label={t("loadingProducts")}>
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-24 rounded-2xl" />
            ))}
          </div>
        ) : (
          <>
            <CategoryChips categories={categories} activeId={categoryId} onSelect={setActiveCategory} />
            {visible.length === 0 ? (
              <p className="py-8 text-center text-base text-muted-foreground">{t("noProducts")}</p>
            ) : (
              <ProductGrid
                products={visible}
                hasOptions={(id) => {
                  const lookup = optionsFor(id)
                  return lookup?.kind === "ready" ? lookup.groups.length > 0 : undefined
                }}
                quantityInCart={(id) => quantities.get(id) ?? 0}
                resolvingId={resolvingId}
                flashId={flashId}
                onTap={(product) => void handleTap(product)}
              />
            )}
          </>
        )}

        <CartBar
          count={lines.length}
          total={total}
          sending={sending}
          error={error}
          onOpenCart={() => setCartOpen(true)}
          onSend={() => void send()}
          onRetry={() => void send()}
        />
      </div>

      {/* Tablet landscape / desktop: the cart is always visible on the right. */}
      <aside
        aria-label={t("cart.title")}
        className="bg-card sticky top-4 hidden max-h-[calc(100dvh-7rem)] flex-col overflow-hidden rounded-2xl border-2 lg:flex"
      >
        <div className="border-b px-4 py-3">
          <h2 className="text-lg font-bold">
            {t("cart.title")} · {table.name}
          </h2>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-4">
          <CartLines
            lines={lines}
            disabled={sending}
            onChangeQuantity={(id, delta) => editLines((current) => changePendingQuantity(current, id, delta))}
            onRemove={(id) => editLines((current) => removePendingLine(current, id))}
          />
        </div>
        <div className="space-y-3 border-t px-4 py-3">
          <div className="flex items-baseline justify-between">
            <span className="text-base text-muted-foreground">{t("cart.total")}</span>
            <span className="text-2xl font-bold tabular-nums">{formatMoney(total)}</span>
          </div>
          <SendError error={error} onRetry={() => void send()} sending={sending} />
          <SendButton count={lines.length} sending={sending} onSend={() => void send()} className="w-full" />
        </div>
      </aside>

      {picker && (
        <OptionPanel
          key={picker.product.id}
          product={picker.product}
          groups={picker.groups}
          onCancel={() => setPicker(null)}
          onConfirm={(options) => {
            addToCart(picker.product, options)
            setPicker(null)
          }}
        />
      )}

      <TouchSheet
        open={cartOpen}
        onOpenChange={setCartOpen}
        layout="bottom"
        title={`${t("cart.title")} · ${table.name}`}
        closeLabel={t("cart.close")}
        footer={
          <div className="space-y-3">
            <div className="flex items-baseline justify-between">
              <span className="text-base text-muted-foreground">{t("cart.total")}</span>
              <span className="text-2xl font-bold tabular-nums">{formatMoney(total)}</span>
            </div>
            <SendError error={error} onRetry={() => void send()} sending={sending} />
            <SendButton count={lines.length} sending={sending} onSend={() => void send()} className="w-full" />
          </div>
        }
      >
        <div className="px-4">
          <CartLines
            lines={lines}
            disabled={sending}
            onChangeQuantity={(id, delta) => editLines((current) => changePendingQuantity(current, id, delta))}
            onRemove={(id) => editLines((current) => removePendingLine(current, id))}
          />
        </div>
      </TouchSheet>

      {success && (
        <TouchSheet
          open
          onOpenChange={(next) => {
            if (!next) setSuccess(null)
          }}
          title={t("success.title")}
          closeLabel={t("cart.close")}
          footer={
            <div className="flex gap-3">
              <button
                type="button"
                onClick={() => setSuccess(null)}
                className="min-h-14 flex-1 rounded-xl border-2 text-base font-semibold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
              >
                {t("success.continue")}
              </button>
              <button
                type="button"
                autoFocus
                onClick={onBackToTables}
                className="bg-primary text-primary-foreground min-h-14 flex-1 rounded-xl text-base font-bold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
              >
                {t("success.backToTables")}
              </button>
            </div>
          }
        >
          <div role="status" className="flex flex-col items-center gap-3 px-4 py-6 text-center">
            <CircleCheckBig className="text-status-success-fg size-14 motion-safe:animate-in motion-safe:zoom-in-75" aria-hidden="true" />
            <p className="text-lg font-semibold tabular-nums">
              {t("success.body", { table: table.name, count: success.count, total: formatMoney(success.total) })}
            </p>
            {success.joined && <p className="text-base text-muted-foreground">{t("success.occupiedJoined")}</p>}
          </div>
        </TouchSheet>
      )}

      <TouchConfirm
        open={leaveOpen}
        onOpenChange={setLeaveOpen}
        title={t("leave.title")}
        body={t("leave.body", { count: lines.length })}
        cancelLabel={t("leave.stay")}
        confirmLabel={t("leave.discard")}
        closeLabel={t("cart.close")}
        destructive
        onConfirm={() => {
          setLeaveOpen(false)
          onBackToTables()
        }}
      />
    </div>
  )
}
