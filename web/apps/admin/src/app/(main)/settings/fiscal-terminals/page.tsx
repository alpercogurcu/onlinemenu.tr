"use client"

import axios from "axios"
import {
  CircleCheck,
  CircleX,
  List,
  Loader2,
  Pencil,
  Plus,
  RefreshCw,
  Router as RouterIcon,
  Zap,
} from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Select, SelectItem } from "@/components/ui/select"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  useCreateFiscalTerminal,
  useFiscalTerminals,
  useSyncFiscalSections,
  useUpdateFiscalTerminal,
} from "@/hooks/use-fiscal"
import { useBranches } from "@/hooks/use-tenant"
import { useAuthStore } from "@/store/auth-store"
import type { BasketMode, FiscalTerminal } from "@/types"

interface TerminalFormState {
  qr: string
  label: string
  basket_mode: BasketMode
}

const defaultForm: TerminalFormState = { qr: "", label: "", basket_mode: "instant" }

// Mirrors backend/internal/modules/payment/http/fiscal_handler.go#parseQR —
// client-side parsing is preview-only, the server re-parses and validates the
// authoritative value.
function parseQR(qr: string): { merchantRef: string; branchRef: string; serial: string } | null {
  const parts = qr.trim().split("_")
  if (parts.length !== 3 || parts.some((p) => p.trim() === "")) return null
  const [merchantRef, branchRef, serial] = parts
  return { merchantRef, branchRef, serial }
}

function basketModeBadge(mode: BasketMode) {
  return mode === "instant" ? (
    <Badge variant="outline" className="bg-green-100 text-green-700 border-green-200">
      <Zap className="size-3" />
      Hemen öde
    </Badge>
  ) : (
    <Badge variant="outline" className="bg-blue-100 text-blue-700 border-blue-200">
      <List className="size-3" />
      Cihazda listele
    </Badge>
  )
}

function statusBadge(isActive: boolean) {
  return (
    <Badge
      variant="outline"
      className={
        isActive
          ? "bg-green-100 text-green-700 border-green-200"
          : "bg-gray-100 text-gray-600 border-gray-200"
      }
    >
      {isActive ? <CircleCheck className="size-3" /> : <CircleX className="size-3" />}
      {isActive ? "Aktif" : "Pasif"}
    </Badge>
  )
}

export default function FiscalTerminalsPage() {
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""
  const { data: branches } = useBranches(tenantId)
  const [branchId, setBranchId] = useState("")

  useEffect(() => {
    if (!branchId && branches && branches.length > 0) {
      setBranchId(branches[0].id)
    }
  }, [branches, branchId])

  const { data, isLoading } = useFiscalTerminals(branchId)
  const terminals = data ?? []

  const createTerminal = useCreateFiscalTerminal()
  const updateTerminal = useUpdateFiscalTerminal()
  const syncSections = useSyncFiscalSections()

  // sync-sections never returns a per-terminal "last synced" timestamp on the
  // terminal itself (terminalResponse has no such field — only each device
  // section carries its own synced_at). This session-local map lets the row
  // show a "last synced" moment for terminals synced since the page loaded;
  // it intentionally does not survive a reload.
  const [lastSyncMap, setLastSyncMap] = useState<Record<string, string>>({})

  const [sheetOpen, setSheetOpen] = useState(false)
  const [mode, setMode] = useState<"create" | "edit">("create")
  const [editingTerminal, setEditingTerminal] = useState<FiscalTerminal | null>(null)
  const [form, setForm] = useState<TerminalFormState>(defaultForm)
  const [fieldErrors, setFieldErrors] = useState<{ qr?: string; label?: string }>({})

  const [confirmTarget, setConfirmTarget] = useState<FiscalTerminal | null>(null)

  const qrPreview = useMemo(() => (form.qr ? parseQR(form.qr) : null), [form.qr])

  const handleOpenCreate = () => {
    setMode("create")
    setEditingTerminal(null)
    setForm(defaultForm)
    setFieldErrors({})
    setSheetOpen(true)
  }

  const handleOpenEdit = (terminal: FiscalTerminal) => {
    setMode("edit")
    setEditingTerminal(terminal)
    setForm({ qr: "", label: terminal.label, basket_mode: terminal.basket_mode })
    setFieldErrors({})
    setSheetOpen(true)
  }

  const isSubmitting = mode === "create" ? createTerminal.isPending : updateTerminal.isPending

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()

    const errors: { qr?: string; label?: string } = {}
    if (mode === "create" && !qrPreview) {
      errors.qr = "QR formatı geçersiz. Beklenen format: merchantRef_branchRef_terminalSerial"
    }
    if (!form.label.trim()) {
      errors.label = "Etiket zorunludur"
    }
    setFieldErrors(errors)
    if (Object.keys(errors).length > 0) return

    try {
      if (mode === "create") {
        await createTerminal.mutateAsync({
          qr: form.qr.trim(),
          branch_id: branchId,
          label: form.label.trim(),
          basket_mode: form.basket_mode,
        })
        toast.success("Terminal eklendi")
      } else if (editingTerminal) {
        await updateTerminal.mutateAsync({
          id: editingTerminal.id,
          branch_id: branchId,
          label: form.label.trim(),
          basket_mode: form.basket_mode,
        })
        toast.success("Terminal güncellendi")
      }
      setSheetOpen(false)
    } catch (err) {
      const serverMessage =
        axios.isAxiosError(err) && typeof err.response?.data === "string" ? err.response.data : null

      if (serverMessage && serverMessage.toLowerCase().includes("qr")) {
        setFieldErrors((fe) => ({ ...fe, qr: serverMessage }))
        return
      }
      if (serverMessage && serverMessage.toLowerCase().includes("serial")) {
        toast.error("Bu terminal seri numarası zaten kayıtlı")
        return
      }
      toast.error(
        serverMessage ?? (mode === "create" ? "Terminal eklenemedi" : "Terminal güncellenemedi"),
      )
    }
  }

  const handleConfirmToggle = async () => {
    if (!confirmTarget) return
    const next = !confirmTarget.is_active
    try {
      await updateTerminal.mutateAsync({ id: confirmTarget.id, branch_id: branchId, is_active: next })
      toast.success(next ? "Terminal aktif edildi" : "Terminal pasif edildi")
      setConfirmTarget(null)
    } catch {
      toast.error("Durum güncellenemedi")
    }
  }

  const handleSync = (terminal: FiscalTerminal) => {
    syncSections.mutate(terminal.id, {
      onSuccess: (res) => {
        setLastSyncMap((m) => ({ ...m, [terminal.id]: new Date().toISOString() }))
        toast.success(`${res.data.length} kısım senkronize edildi`)
      },
      onError: () => {
        toast.error("Kısımlar senkronize edilemedi", {
          action: { label: "Tekrar dene", onClick: () => handleSync(terminal) },
        })
      },
    })
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Fiscal Terminaller</h1>
          <p className="text-muted-foreground">
            Şubelere bağlı Token X mali cihazlarını yönetin.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Select
            value={branchId}
            onChange={(e) => setBranchId(e.target.value)}
            className="w-48"
            aria-label="Şube seçin"
          >
            <SelectItem value="">Şube seçin</SelectItem>
            {(branches ?? []).map((b) => (
              <SelectItem key={b.id} value={b.id}>
                {b.name}
              </SelectItem>
            ))}
          </Select>
          <Button onClick={handleOpenCreate} disabled={!branchId}>
            <Plus className="size-4" />
            Terminal Ekle
          </Button>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Terminal Listesi</CardTitle>
          <CardDescription>
            {branchId ? `Toplam ${terminals.length} terminal.` : "Terminalleri görmek için bir şube seçin."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {!branchId ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <RouterIcon className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Şube seçilmedi</h3>
              <p className="text-sm text-muted-foreground mt-1">
                Terminalleri listelemek için önce bir şube seçin.
              </p>
            </div>
          ) : isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-12 w-full" />
              ))}
            </div>
          ) : terminals.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <RouterIcon className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Terminal bulunamadı</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">
                Bu şubeye ilk mali cihazı ekleyerek başlayın.
              </p>
              <Button onClick={handleOpenCreate}>
                <Plus className="size-4" />
                İlk terminali ekle
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Etiket</TableHead>
                  <TableHead>Seri No</TableHead>
                  <TableHead>Sepet Modu</TableHead>
                  <TableHead>Durum</TableHead>
                  <TableHead>Son Kısım Senkronu</TableHead>
                  <TableHead className="w-[1%] whitespace-nowrap">İşlemler</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {terminals.map((terminal) => {
                  const isSyncingThis = syncSections.isPending && syncSections.variables === terminal.id
                  const lastSynced = lastSyncMap[terminal.id]
                  return (
                    <TableRow key={terminal.id}>
                      <TableCell className="font-medium">{terminal.label || "—"}</TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {terminal.terminal_serial}
                      </TableCell>
                      <TableCell>{basketModeBadge(terminal.basket_mode)}</TableCell>
                      <TableCell>{statusBadge(terminal.is_active)}</TableCell>
                      <TableCell className="text-muted-foreground text-sm">
                        {lastSynced ? new Date(lastSynced).toLocaleString("tr-TR") : "—"}
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center justify-end gap-1 whitespace-nowrap">
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => handleSync(terminal)}
                            disabled={isSyncingThis}
                          >
                            {isSyncingThis ? (
                              <Loader2 className="size-4 animate-spin" />
                            ) : (
                              <RefreshCw className="size-4" />
                            )}
                            Kısımları Senkronla
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => handleOpenEdit(terminal)}
                            aria-label={`${terminal.label || terminal.terminal_serial} düzenle`}
                          >
                            <Pencil className="size-4" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => setConfirmTarget(terminal)}
                            aria-label={
                              terminal.is_active
                                ? `${terminal.label || terminal.terminal_serial} pasif et`
                                : `${terminal.label || terminal.terminal_serial} aktif et`
                            }
                          >
                            {terminal.is_active ? (
                              <CircleX className="size-4 text-destructive" />
                            ) : (
                              <CircleCheck className="size-4 text-green-600" />
                            )}
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
        <SheetContent>
          <SheetHeader>
            <SheetTitle>{mode === "create" ? "Yeni Terminal" : "Terminali Düzenle"}</SheetTitle>
            <SheetDescription>
              {mode === "create"
                ? "Cihazın QR kodunu okutup terminali seçili şubeye bağlayın."
                : `"${editingTerminal?.label || editingTerminal?.terminal_serial}" terminalinin bilgilerini güncelleyin.`}
            </SheetDescription>
          </SheetHeader>
          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            {mode === "create" && (
              <div className="space-y-2">
                <Label htmlFor="terminal-qr">QR Kodu</Label>
                <Input
                  id="terminal-qr"
                  placeholder="merchantRef_branchRef_terminalSerial"
                  value={form.qr}
                  onChange={(e) => {
                    setForm((f) => ({ ...f, qr: e.target.value }))
                    setFieldErrors((fe) => ({ ...fe, qr: undefined }))
                  }}
                  aria-invalid={Boolean(fieldErrors.qr)}
                />
                {fieldErrors.qr ? (
                  <p className="text-sm text-destructive">{fieldErrors.qr}</p>
                ) : qrPreview ? (
                  <div className="space-y-1 rounded-md border bg-muted/50 p-3 text-xs text-muted-foreground">
                    <p>
                      Üye İşyeri Ref:{" "}
                      <span className="font-mono text-foreground">{qrPreview.merchantRef}</span>
                    </p>
                    <p>
                      Şube Ref: <span className="font-mono text-foreground">{qrPreview.branchRef}</span>
                    </p>
                    <p>
                      Terminal Seri No:{" "}
                      <span className="font-mono text-foreground">{qrPreview.serial}</span>
                    </p>
                  </div>
                ) : (
                  <p className="text-sm text-muted-foreground">
                    Format: merchantRef_branchRef_terminalSerial
                  </p>
                )}
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="terminal-label">Etiket</Label>
              <Input
                id="terminal-label"
                placeholder="örn: Kasa 1"
                value={form.label}
                onChange={(e) => {
                  setForm((f) => ({ ...f, label: e.target.value }))
                  setFieldErrors((fe) => ({ ...fe, label: undefined }))
                }}
                aria-invalid={Boolean(fieldErrors.label)}
              />
              {fieldErrors.label && <p className="text-sm text-destructive">{fieldErrors.label}</p>}
            </div>
            <div className="space-y-2">
              <Label>Sepet Modu</Label>
              <RadioGroup
                name="basket_mode"
                value={form.basket_mode}
                onValueChange={(v) => setForm((f) => ({ ...f, basket_mode: v as BasketMode }))}
              >
                <label
                  htmlFor="basket-mode-instant"
                  className="flex items-start gap-3 rounded-md border p-3 cursor-pointer has-[:checked]:border-primary has-[:checked]:bg-accent/50"
                >
                  <RadioGroupItem value="instant" id="basket-mode-instant" className="mt-0.5" />
                  <span>
                    <span className="block text-sm font-medium">Hemen öde (önerilen)</span>
                    <span className="block text-xs text-muted-foreground">
                      Her ödeme anında tekil olarak cihaza gönderilir.
                    </span>
                  </span>
                </label>
                <label
                  htmlFor="basket-mode-list"
                  className="flex items-start gap-3 rounded-md border p-3 cursor-pointer has-[:checked]:border-primary has-[:checked]:bg-accent/50"
                >
                  <RadioGroupItem value="list" id="basket-mode-list" className="mt-0.5" />
                  <span>
                    <span className="block text-sm font-medium">Cihazda listele</span>
                    <span className="block text-xs text-muted-foreground">
                      Adisyon kapanışında sepet toplu olarak cihaza gönderilir.
                    </span>
                  </span>
                </label>
              </RadioGroup>
            </div>
            <Button type="submit" className="w-full" disabled={isSubmitting}>
              {isSubmitting ? "Kaydediliyor..." : "Kaydet"}
            </Button>
          </form>
        </SheetContent>
      </Sheet>

      <Dialog open={confirmTarget !== null} onOpenChange={(open) => !open && setConfirmTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {confirmTarget?.is_active ? "Terminali pasif et" : "Terminali aktif et"}
            </DialogTitle>
            <DialogDescription>
              {confirmTarget?.is_active
                ? `"${confirmTarget?.label || confirmTarget?.terminal_serial}" terminali pasif hale getirilecek. Bu terminal üzerinden yeni mali kayıt gönderilemez.`
                : `"${confirmTarget?.label || confirmTarget?.terminal_serial}" terminali aktif hale getirilecek.`}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmTarget(null)}>
              Vazgeç
            </Button>
            <Button
              variant={confirmTarget?.is_active ? "destructive" : "default"}
              onClick={handleConfirmToggle}
              disabled={updateTerminal.isPending}
            >
              {updateTerminal.isPending ? "Kaydediliyor..." : "Onayla"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
