"use client"

import { MoreHorizontal, UserCheck, UserX } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import { ConfirmDialog } from "@/components/catalog/confirm-dialog"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useUpdateMembershipStatus } from "@/hooks/use-identity"
import type { Membership } from "@/types"

interface UserRowActionsProps {
  tenantId: string
  member: Membership
}

// Only the actions the backend actually exposes: membership status. Role
// change and invite resend have no endpoint yet, so they are deliberately
// absent rather than rendered as dead buttons.
export function UserRowActions({ tenantId, member }: UserRowActionsProps) {
  const update = useUpdateMembershipStatus(tenantId)
  const [confirm, setConfirm] = useState<"suspend" | "activate" | null>(null)

  if (member.status === "terminated") return null

  const isActive = member.status === "active"
  const label = member.person_name || member.person_email

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" aria-label={`${label} için işlemler`}>
            <MoreHorizontal className="size-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {isActive ? (
            <DropdownMenuItem onSelect={() => setConfirm("suspend")}>
              <UserX className="size-4" />
              Pasife al
            </DropdownMenuItem>
          ) : (
            <DropdownMenuItem onSelect={() => setConfirm("activate")}>
              <UserCheck className="size-4" />
              Aktife al
            </DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>

      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => {
          if (!open) setConfirm(null)
        }}
        title={confirm === "suspend" ? `${label} pasife alınsın mı?` : `${label} aktife alınsın mı?`}
        description={
          confirm === "suspend"
            ? "Kullanıcı giriş yapamaz; üyelik silinmez, istediğiniz zaman geri açabilirsiniz."
            : "Kullanıcı yeniden giriş yapabilir ve rolündeki yetkilere kavuşur."
        }
        confirmLabel={confirm === "suspend" ? "Pasife al" : "Aktife al"}
        cancelLabel="Vazgeç"
        destructive={confirm === "suspend"}
        onConfirm={async () => {
          await update.mutateAsync({
            membershipId: member.id,
            status: confirm === "suspend" ? "suspended" : "active",
          })
          toast.success(confirm === "suspend" ? `${label} pasife alındı` : `${label} aktife alındı`)
          setConfirm(null)
        }}
        onError={() => toast.error("Üyelik durumu güncellenemedi")}
      />
    </>
  )
}
