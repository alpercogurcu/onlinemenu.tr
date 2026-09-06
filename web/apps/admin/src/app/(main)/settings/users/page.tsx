"use client"

import axios from "axios"
import { AlertTriangle, CheckCircle2, Plus, Search, Users } from "lucide-react"
import { useMemo, useState } from "react"
import { toast } from "sonner"

import { UserRowActions } from "@/components/settings/user-row-actions"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select } from "@/components/ui/select"
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
import { useMemberships } from "@/hooks/use-identity"
import { useBranches } from "@/hooks/use-tenant"
import api from "@/lib/api"
import { membershipStatusVariant } from "@/lib/status-badge"
import { cn } from "@/lib/utils"
import { useAuthStore } from "@/store/auth-store"
import type { Membership, MembershipStatus } from "@/types"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

export const PAGE_SIZE = 25

const STATUS_LABELS: Record<MembershipStatus, string> = {
  active: "Aktif",
  suspended: "Pasif",
  terminated: "Ayrıldı",
}

export function statusLabel(status: string): string {
  return STATUS_LABELS[status as MembershipStatus] ?? status
}

// "Ayşe Yılmaz" → "AY"; falls back to the e-mail's first letter for a person
// whose name is still empty (invited but never completed their profile).
export function initials(name: string, email: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  if (parts.length === 0) return (email[0] ?? "?").toLocaleUpperCase("tr-TR")
  const first = parts[0][0] ?? ""
  const last = parts.length > 1 ? (parts[parts.length - 1][0] ?? "") : ""
  return `${first}${last}`.toLocaleUpperCase("tr-TR")
}

export interface MemberFilters {
  query: string
  roleId: string // "" = all
  branchId: string // "" = all, "chain" = chain-wide only
}

export function filterMembers(members: Membership[], filters: MemberFilters): Membership[] {
  const q = filters.query.trim().toLocaleLowerCase("tr-TR")
  return members.filter((m) => {
    if (filters.roleId && m.role_id !== filters.roleId) return false
    if (filters.branchId === "chain" && m.branch_id) return false
    if (filters.branchId && filters.branchId !== "chain" && m.branch_id !== filters.branchId) return false
    if (!q) return true
    return (
      m.person_name.toLocaleLowerCase("tr-TR").includes(q) ||
      m.person_email.toLocaleLowerCase("tr-TR").includes(q)
    )
  })
}

// Mirrors settings/roles/page.tsx's local Role shape. branch_id and
// branch_scoped both feed the invite form: per ADR-SEC-005,
// MembershipService.RequiresBranch() is `branch_scoped || branch_id != nil`
// (a role can be branch_id-pinned without the branch_scoped flag set), so
// the client must check both to match what the server actually enforces.
interface Role {
  id: string
  name: string
  branch_id?: string
  branch_scoped: boolean
}

function roleRequiresBranch(role: Role | undefined): boolean {
  if (!role) return false
  return role.branch_scoped || Boolean(role.branch_id)
}

interface RoleListResponse {
  roles: Role[]
}

interface StaffInvitePerson {
  id: string
  keycloak_sub: string
  email: string
  full_name: string
  phone: string
}

interface StaffInviteResult {
  person: StaffInvitePerson
  membership: Membership
  keycloak_user_created: boolean
  notification_sent: boolean
  notification_error?: string
}

// inviteNeedsAction reports whether the admin still has work to do before the
// invited person can log in.
//
// Deliberately NOT `!notification_sent`: the backend only sends a
// password-setup mail for an account it actually created. When an existing
// Keycloak account is reused (the same person already works at another
// tenant), no mail is sent and none is needed — that person logs in with the
// password they already have. Keying the warning off notification_sent alone
// would flag that perfectly normal outcome as a failure.
function inviteNeedsAction(result: StaffInviteResult): boolean {
  return result.keycloak_user_created && !result.notification_sent
}

interface InviteFormState {
  full_name: string
  email: string
  role_id: string
  branch_id: string // "" means chain-wide (no branch)
}

const defaultInviteForm: InviteFormState = {
  full_name: "",
  email: "",
  role_id: "",
  branch_id: "",
}

function useRoles(tenantId: string) {
  return useQuery({
    queryKey: ["roles", tenantId],
    queryFn: async () => {
      const { data } = await api.get<RoleListResponse>(`/v1/identity/${tenantId}/roles`)
      return data
    },
    enabled: Boolean(tenantId),
  })
}

function useInviteStaff(tenantId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: {
      full_name: string
      email: string
      branch_id?: string
      role_id: string
    }) => {
      const { data } = await api.post<StaffInviteResult>(`/v1/identity/${tenantId}/staff`, body)
      return data
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["memberships", tenantId] })
    },
  })
}

// Identity module's writeError always responds with a JSON {"error": "..."}
// body (see backend/internal/modules/identity/http/routes.go writeError),
// unlike some other modules that write plain text — so this only needs the
// object shape.
function extractServerError(err: unknown): string | null {
  if (!axios.isAxiosError(err)) return null
  const data: unknown = err.response?.data
  if (data && typeof data === "object" && "error" in data) {
    const message = (data as { error?: unknown }).error
    return typeof message === "string" ? message : null
  }
  return null
}

export default function UsersPage() {
  const tenantId = useAuthStore((s) => s.tenantId) ?? ""

  const { data: members = [], isLoading } = useMemberships(tenantId)

  const { data: roleData } = useRoles(tenantId)
  const roles = roleData?.roles ?? []
  const roleById = new Map(roles.map((role) => [role.id, role]))

  const { data: branches = [] } = useBranches(tenantId)
  const branchById = new Map(branches.map((branch) => [branch.id, branch]))

  const [filters, setFilters] = useState<MemberFilters>({ query: "", roleId: "", branchId: "" })
  const [page, setPage] = useState(0)

  const filtered = useMemo(() => filterMembers(members, filters), [members, filters])
  const pageCount = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const currentPage = Math.min(page, pageCount - 1)
  const pageRows = filtered.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE)

  const activeCount = members.filter((m) => m.status === "active").length
  const suspendedCount = members.filter((m) => m.status === "suspended").length

  const updateFilters = (patch: Partial<MemberFilters>) => {
    setFilters((f) => ({ ...f, ...patch }))
    setPage(0)
  }

  const inviteStaff = useInviteStaff(tenantId)

  const [sheetOpen, setSheetOpen] = useState(false)
  const [form, setForm] = useState<InviteFormState>(defaultInviteForm)
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<keyof InviteFormState, string>>>({})
  const [inviteResult, setInviteResult] = useState<StaffInviteResult | null>(null)

  const selectedRole = roleById.get(form.role_id)
  const branchRequired = roleRequiresBranch(selectedRole)

  const openSheet = () => {
    setForm(defaultInviteForm)
    setFieldErrors({})
    setInviteResult(null)
    setSheetOpen(true)
  }

  const handleSheetOpenChange = (open: boolean) => {
    setSheetOpen(open)
    if (!open) {
      setForm(defaultInviteForm)
      setFieldErrors({})
      setInviteResult(null)
    }
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()

    const errors: Partial<Record<keyof InviteFormState, string>> = {}
    if (!form.full_name.trim()) errors.full_name = "Ad soyad zorunludur"
    if (!form.email.trim() || !form.email.includes("@")) errors.email = "Geçerli bir e-posta girin"
    if (!form.role_id) errors.role_id = "Rol seçimi zorunludur"
    if (branchRequired && !form.branch_id) {
      errors.branch_id = "Bu rol şubeye bağlıdır (ADR-SEC-005) — şube seçimi zorunludur"
    }
    setFieldErrors(errors)
    if (Object.keys(errors).length > 0) return

    try {
      const result = await inviteStaff.mutateAsync({
        full_name: form.full_name.trim(),
        email: form.email.trim().toLowerCase(),
        role_id: form.role_id,
        branch_id: form.branch_id || undefined,
      })
      // Never a plain success toast here: notification_sent=false means the
      // account exists but the person cannot log in yet, and that has to be
      // surfaced deliberately (docs/lessons-from-b2b.md) rather than folded
      // into a generic "kaydedildi" toast. The result panel below stays
      // open until the admin dismisses it.
      setInviteResult(result)
    } catch (err) {
      const serverMessage = extractServerError(err)
      if (serverMessage && serverMessage.toLowerCase().includes("branch")) {
        setFieldErrors((fe) => ({ ...fe, branch_id: serverMessage }))
        return
      }
      toast.error(serverMessage ?? "Personel eklenemedi")
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Kullanıcılar</h1>
          <p className="text-sm text-muted-foreground" data-testid="members-summary">
            {isLoading
              ? "Üyeler yükleniyor…"
              : `${members.length} üye · ${activeCount} aktif` +
                (suspendedCount > 0 ? ` · ${suspendedCount} pasif` : "")}
          </p>
        </div>
        <Button onClick={openSheet}>
          <Plus className="size-4" />
          Personel davet et
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative w-full sm:w-72">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            aria-label="Ad veya e-posta ara"
            placeholder="Ad veya e-posta ara…"
            className="pl-9"
            value={filters.query}
            onChange={(e) => updateFilters({ query: e.target.value })}
          />
        </div>
        <Select
          aria-label="Rol süzgeci"
          className="w-auto"
          value={filters.roleId}
          onChange={(e) => updateFilters({ roleId: e.target.value })}
        >
          <option value="">Tüm roller</option>
          {roles.map((role) => (
            <option key={role.id} value={role.id}>
              {role.name}
            </option>
          ))}
        </Select>
        <Select
          aria-label="Şube süzgeci"
          className="w-auto"
          value={filters.branchId}
          onChange={(e) => updateFilters({ branchId: e.target.value })}
        >
          <option value="">Tüm şubeler</option>
          <option value="chain">Zincir geneli</option>
          {branches.map((branch) => (
            <option key={branch.id} value={branch.id}>
              {branch.name}
            </option>
          ))}
        </Select>
      </div>

      <Card>
        <CardContent className="p-0">
          {isLoading ? (
            <div className="space-y-3 p-6">
              {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : members.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Users className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Üye bulunamadı</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">
                İşletmenize personel davet edin.
              </p>
              <Button onClick={openSheet}>
                <Plus className="size-4" />
                İlk personeli ekle
              </Button>
            </div>
          ) : filtered.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <Search className="size-10 text-muted-foreground mb-3" />
              <h3 className="text-base font-semibold">Süzgece uyan üye yok</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">
                Aramayı veya rol/şube seçimini değiştirin.
              </p>
              <Button
                variant="outline"
                onClick={() => updateFilters({ query: "", roleId: "", branchId: "" })}
              >
                Süzgeçleri temizle
              </Button>
            </div>
          ) : (
            <>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Kişi</TableHead>
                    <TableHead>Rol</TableHead>
                    <TableHead>Şube</TableHead>
                    <TableHead>Durum</TableHead>
                    <TableHead className="w-12">
                      <span className="sr-only">İşlemler</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {pageRows.map((member) => (
                    <TableRow key={member.id}>
                      <TableCell>
                        <div className="flex items-center gap-3">
                          <Avatar className="size-8">
                            <AvatarFallback className="text-xs font-semibold">
                              {initials(member.person_name, member.person_email)}
                            </AvatarFallback>
                          </Avatar>
                          <div className="min-w-0">
                            <div className="truncate font-medium">
                              {member.person_name || member.person_email}
                            </div>
                            {member.person_name && (
                              <div className="truncate text-xs text-muted-foreground">
                                {member.person_email}
                              </div>
                            )}
                          </div>
                        </div>
                      </TableCell>
                      <TableCell className="text-sm">
                        {member.role_name || roleById.get(member.role_id)?.name || (
                          <span className="text-muted-foreground text-xs font-mono">
                            {member.role_id.slice(0, 8)}…
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="text-muted-foreground text-sm">
                        {member.branch_id ? (branchById.get(member.branch_id)?.name ?? "—") : "Tüm şubeler"}
                      </TableCell>
                      <TableCell>
                        <Badge variant={membershipStatusVariant(member.status)}>
                          {statusLabel(member.status)}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-right">
                        <UserRowActions tenantId={tenantId} member={member} />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              {pageCount > 1 && (
                <nav
                  aria-label="Sayfalama"
                  className="flex items-center justify-between border-t px-4 py-3 text-sm text-muted-foreground"
                >
                  <span>
                    {currentPage * PAGE_SIZE + 1}–{Math.min((currentPage + 1) * PAGE_SIZE, filtered.length)} /{" "}
                    {filtered.length}
                  </span>
                  <div className="flex gap-2">
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={currentPage === 0}
                      onClick={() => setPage(currentPage - 1)}
                    >
                      Önceki
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={currentPage >= pageCount - 1}
                      onClick={() => setPage(currentPage + 1)}
                    >
                      Sonraki
                    </Button>
                  </div>
                </nav>
              )}
            </>
          )}
        </CardContent>
      </Card>

      <Sheet open={sheetOpen} onOpenChange={handleSheetOpenChange}>
        <SheetContent>
          <SheetHeader>
            <SheetTitle>Personel davet et</SheetTitle>
            <SheetDescription>
              Yeni bir personeli işletmenize davet edin. Şifre belirleme e-postası otomatik gönderilir.
            </SheetDescription>
          </SheetHeader>

          {inviteResult ? (
            <div className="mt-6 space-y-4">
              <div
                className={cn(
                  "space-y-2 rounded-md border p-4 text-sm",
                  inviteNeedsAction(inviteResult)
                    ? "border-status-warning-border bg-status-warning-bg text-status-warning-fg"
                    : "border-status-success-border bg-status-success-bg text-status-success-fg",
                )}
              >
                <div className="flex items-center gap-2 font-medium">
                  {inviteNeedsAction(inviteResult) ? (
                    <AlertTriangle className="size-4 shrink-0" />
                  ) : (
                    <CheckCircle2 className="size-4 shrink-0" />
                  )}
                  {inviteNeedsAction(inviteResult)
                    ? "Personel eklendi ama davet e-postası gönderilemedi"
                    : inviteResult.notification_sent
                      ? "Personel eklendi, davet e-postası gönderildi"
                      : "Personel eklendi, mevcut hesap bağlandı"}
                </div>

                <p>
                  <span className="font-medium">{inviteResult.person.full_name}</span>{" "}
                  ({inviteResult.person.email}) hesabı ve işletme üyeliği oluşturuldu.
                </p>

                {!inviteResult.keycloak_user_created && (
                  <p
                    className={
                      inviteNeedsAction(inviteResult) ? "text-status-warning-fg" : "text-status-success-fg"
                    }
                  >
                    Bu e-posta ile sistemde zaten bir Keycloak hesabı vardı (başka bir işletmede
                    çalışıyor veya daha önce davet edilmiş olabilir) — yeni hesap açılmadı, mevcut
                    hesap bu işletmeye bağlandı.
                  </p>
                )}

                {inviteNeedsAction(inviteResult) && (
                  <div className="space-y-1 pt-1">
                    <p>
                      Personel şifresini belirleyemeyecek ve <strong>giriş yapamayacak</strong> —
                      e-posta gönderimi başarısız oldu (örn. realm&apos;de SMTP yapılandırılmamış
                      olabilir).
                    </p>
                    {inviteResult.notification_error && (
                      <p className="rounded bg-status-warning-bg p-2 font-mono text-xs break-all text-status-warning-fg">
                        {inviteResult.notification_error}
                      </p>
                    )}
                    <p className="font-medium">
                      Bu personelin şifresini Keycloak yönetim panelinden elle belirlemeniz gerekiyor.
                    </p>
                  </div>
                )}
              </div>

              <div className="flex gap-2">
                <Button variant="outline" className="flex-1" onClick={openSheet}>
                  Başka personel ekle
                </Button>
                <Button className="flex-1" onClick={() => handleSheetOpenChange(false)}>
                  Kapat
                </Button>
              </div>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="mt-6 space-y-4">
              <div className="space-y-2">
                <Label htmlFor="staff-full-name">Ad Soyad</Label>
                <Input
                  id="staff-full-name"
                  placeholder="örn: Ayşe Yılmaz"
                  value={form.full_name}
                  onChange={(e) => {
                    setForm((f) => ({ ...f, full_name: e.target.value }))
                    setFieldErrors((fe) => ({ ...fe, full_name: undefined }))
                  }}
                  aria-invalid={Boolean(fieldErrors.full_name)}
                />
                {fieldErrors.full_name && (
                  <p className="text-sm text-destructive">{fieldErrors.full_name}</p>
                )}
              </div>

              <div className="space-y-2">
                <Label htmlFor="staff-email">E-posta</Label>
                <Input
                  id="staff-email"
                  type="email"
                  placeholder="ayse@ornek.com"
                  value={form.email}
                  onChange={(e) => {
                    setForm((f) => ({ ...f, email: e.target.value }))
                    setFieldErrors((fe) => ({ ...fe, email: undefined }))
                  }}
                  aria-invalid={Boolean(fieldErrors.email)}
                />
                {fieldErrors.email && <p className="text-sm text-destructive">{fieldErrors.email}</p>}
              </div>

              <div className="space-y-2">
                <Label htmlFor="staff-role">Rol</Label>
                <Select
                  id="staff-role"
                  value={form.role_id}
                  onChange={(e) => {
                    const roleId = e.target.value
                    // A previously chosen branch is always kept: it is only
                    // ever *required* for a branch-scoped role, never
                    // *forbidden* for a chain-wide one (a chain-wide role
                    // pinned to one branch is a legal membership).
                    setForm((f) => ({ ...f, role_id: roleId }))
                    setFieldErrors((fe) => ({ ...fe, role_id: undefined, branch_id: undefined }))
                  }}
                  aria-invalid={Boolean(fieldErrors.role_id)}
                >
                  <option value="">Rol seçin</option>
                  {roles.map((role) => (
                    <option key={role.id} value={role.id}>
                      {role.name}
                      {roleRequiresBranch(role) ? " (şubeye bağlı)" : ""}
                    </option>
                  ))}
                </Select>
                {fieldErrors.role_id && <p className="text-sm text-destructive">{fieldErrors.role_id}</p>}
              </div>

              <div className="space-y-2">
                <Label htmlFor="staff-branch">
                  Şube{branchRequired ? " (zorunlu)" : " (opsiyonel — boş bırakılırsa tüm şubeler)"}
                </Label>
                <Select
                  id="staff-branch"
                  value={form.branch_id}
                  onChange={(e) => {
                    setForm((f) => ({ ...f, branch_id: e.target.value }))
                    setFieldErrors((fe) => ({ ...fe, branch_id: undefined }))
                  }}
                  aria-invalid={Boolean(fieldErrors.branch_id)}
                >
                  <option value="">
                    {branchRequired ? "Şube seçin" : "Tüm şubeler (zincir geneli)"}
                  </option>
                  {branches.map((branch) => (
                    <option key={branch.id} value={branch.id}>
                      {branch.name}
                    </option>
                  ))}
                </Select>
                {fieldErrors.branch_id ? (
                  <p className="text-sm text-destructive">{fieldErrors.branch_id}</p>
                ) : branchRequired ? (
                  <p className="text-sm text-muted-foreground">
                    Seçilen rol şubeye bağlı (ADR-SEC-005) — zincir geneli atanamaz.
                  </p>
                ) : null}
              </div>

              <Button type="submit" className="w-full" disabled={inviteStaff.isPending}>
                {inviteStaff.isPending ? "Davet gönderiliyor..." : "Davet Gönder"}
              </Button>
            </form>
          )}
        </SheetContent>
      </Sheet>
    </div>
  )
}
