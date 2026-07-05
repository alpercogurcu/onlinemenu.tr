"use client"

import { useRouter, useSearchParams } from "next/navigation"
import { toast } from "sonner"

import { type ReactNode, useEffect, useRef, useState } from "react"

import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { fetchContexts, fetchMe, selectMembershipContext } from "@/lib/identity-bootstrap"
import { decodeJwtPayload } from "@/lib/jwt"
import { consumePkceParams } from "@/lib/keycloak"
import { setKeycloakTokens, setSelectedMembershipId, tokensFromResponse } from "@/lib/keycloak-token-store"
import { useAuthStore } from "@/store/auth-store"
import type { TenantContext } from "@/types"

type Status = "processing" | "picking" | "no-access" | "error"

function CenteredCard({
  title,
  description,
  children,
}: {
  title: string
  description: string
  children?: ReactNode
}) {
  return (
    <div className="min-h-screen flex items-center justify-center bg-background px-4">
      <Card className="w-full max-w-md">
        <CardHeader className="text-center">
          <CardTitle className="text-xl">{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </CardHeader>
        {children && <CardContent className="space-y-2">{children}</CardContent>}
      </Card>
    </div>
  )
}

export default function AuthCallbackClient() {
  const router = useRouter()
  const searchParams = useSearchParams()
  const { setSession } = useAuthStore()

  const [status, setStatus] = useState<Status>("processing")
  const [contexts, setContexts] = useState<TenantContext[]>([])
  const [keycloakAccessToken, setKeycloakAccessToken] = useState<string | null>(null)
  const [errorMessage, setErrorMessage] = useState("")

  // `code`/PKCE verifier are single-use (consumePkceParams deletes them on
  // read). React StrictMode double-invokes effects in dev, which would
  // otherwise burn the code on the first run and hit the "no PKCE state"
  // error path on the second — this ref survives that double mount/cleanup
  // on the same fiber.
  const startedRef = useRef(false)

  async function completeLogin(
    accessToken: string,
    membershipId: string,
    contextList: TenantContext[],
  ) {
    const ctxToken = await selectMembershipContext(accessToken, membershipId)
    setSelectedMembershipId(membershipId)
    const me = await fetchMe(ctxToken)
    const context = contextList.find((c) => c.membership_id === membershipId)
    setSession(ctxToken, { id: me.id, name: me.full_name, email: me.email }, context?.tenant_id ?? "")
    router.push("/")
  }

  async function run() {
    const kcError = searchParams.get("error")
    if (kcError) {
      setErrorMessage(searchParams.get("error_description") ?? kcError)
      setStatus("error")
      return
    }

    const code = searchParams.get("code")
    const state = searchParams.get("state")
    if (!code || !state) {
      setErrorMessage("Eksik yetkilendirme parametreleri")
      setStatus("error")
      return
    }

    const pkce = consumePkceParams()
    if (!pkce || pkce.state !== state) {
      setErrorMessage("Oturum durumu doğrulanamadı, tekrar giriş yapın")
      setStatus("error")
      return
    }

    try {
      const redirectUri = `${window.location.origin}/auth/callback`
      const res = await fetch("/api/auth/token", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ code, code_verifier: pkce.verifier, redirect_uri: redirectUri }),
      })
      if (!res.ok) {
        throw new Error(`token exchange failed: ${res.status}`)
      }
      const data = await res.json()
      const tokens = tokensFromResponse(data)

      if (tokens.idToken) {
        const payload = decodeJwtPayload<{ nonce?: string }>(tokens.idToken)
        if (payload?.nonce !== pkce.nonce) {
          throw new Error("nonce mismatch — possible replay")
        }
      }

      setKeycloakTokens(tokens)
      setKeycloakAccessToken(tokens.accessToken)

      const list = await fetchContexts(tokens.accessToken)
      setContexts(list)

      if (list.length === 0) {
        setStatus("no-access")
        return
      }
      if (list.length === 1) {
        await completeLogin(tokens.accessToken, list[0].membership_id, list)
        return
      }
      setStatus("picking")
    } catch (err) {
      setErrorMessage(
        err instanceof Error ? err.message : "Giriş sırasında beklenmeyen bir hata oluştu",
      )
      setStatus("error")
    }
  }

  async function handlePick(membershipId: string) {
    if (!keycloakAccessToken) return
    try {
      await completeLogin(keycloakAccessToken, membershipId, contexts)
    } catch {
      toast.error("Bağlam seçilemedi, tekrar deneyin")
      setStatus("error")
    }
  }

  useEffect(() => {
    if (startedRef.current) return
    startedRef.current = true
    void run()
    // Runs once on mount to process the `code`/`state` query params from the
    // Keycloak redirect; searchParams/router identity churn must not re-trigger it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  if (status === "processing") {
    return <CenteredCard title="Giriş yapılıyor" description="Lütfen bekleyin..." />
  }

  if (status === "no-access") {
    return (
      <CenteredCard
        title="Erişim bulunamadı"
        description="Hesabınıza tanımlı bir işletme/şube bulunamadı. Yöneticinizle iletişime geçin."
      >
        <Button className="w-full" onClick={() => router.push("/login")}>
          Giriş sayfasına dön
        </Button>
      </CenteredCard>
    )
  }

  if (status === "error") {
    return (
      <CenteredCard title="Giriş başarısız" description={errorMessage}>
        <Button className="w-full" onClick={() => router.push("/login")}>
          Tekrar dene
        </Button>
      </CenteredCard>
    )
  }

  return (
    <CenteredCard title="Bağlam seçin" description="Devam etmek için işletme/şube seçin">
      {contexts.map((c) => (
        <Button
          key={c.membership_id}
          variant="outline"
          className="w-full justify-start"
          onClick={() => void handlePick(c.membership_id)}
        >
          {c.tenant_name}
          {c.branch_name ? ` · ${c.branch_name}` : ""} — {c.role_name}
        </Button>
      ))}
    </CenteredCard>
  )
}
