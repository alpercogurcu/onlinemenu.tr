"use client"

import { zodResolver } from "@hookform/resolvers/zod"
import axios from "axios"
import { useRouter } from "next/navigation"
import { useForm } from "react-hook-form"
import { toast } from "sonner"
import { z } from "zod"

import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import api from "@/lib/api"
import { buildAuthorizeUrl, callbackRedirectUri, savePkceParams } from "@/lib/keycloak"
import { generateCodeChallenge, generateCodeVerifier, generateNonce, generateState } from "@/lib/pkce"
import { useAuthStore } from "@/store/auth-store"

// Dev-login form (email-only, no password check server-side) is a local
// shortcut and must never be reachable outside development. Defaults to
// enabled so `pnpm dev` keeps working without extra setup; production/
// staging deployments must set NEXT_PUBLIC_ENABLE_DEV_LOGIN=false.
const DEV_LOGIN_ENABLED = process.env.NEXT_PUBLIC_ENABLE_DEV_LOGIN !== "false"

const loginSchema = z.object({
  email: z.string().email("Geçerli bir e-posta adresi girin"),
  password: z.string().min(1, "Şifre gerekli"),
})

type LoginFormValues = z.infer<typeof loginSchema>

interface DevLoginResponse {
  token: string
  tenant_id: string
  user: { id: string; full_name: string; email: string }
}

export default function LoginPage() {
  const router = useRouter()
  const { setSession } = useAuthStore()

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
  })

  const handleKeycloakLogin = async () => {
    try {
      const verifier = generateCodeVerifier()
      const challenge = await generateCodeChallenge(verifier)
      const state = generateState()
      const nonce = generateNonce()
      savePkceParams({ verifier, state, nonce })
      window.location.href = buildAuthorizeUrl({
        redirectUri: callbackRedirectUri(),
        state,
        nonce,
        codeChallenge: challenge,
      })
    } catch {
      toast.error("Giriş başlatılamadı, tekrar deneyin")
    }
  }

  const onSubmit = async (data: LoginFormValues) => {
    try {
      const response = await api.post<DevLoginResponse>("/dev/login", {
        email: data.email,
      })
      const { token, tenant_id, user } = response.data
      setSession(token, { id: user.id, name: user.full_name, email: user.email }, tenant_id)
      toast.success("Giriş başarılı")
      router.push("/")
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.status === 401) {
        toast.error("E-posta veya şifre hatalı")
      } else {
        toast.error("Giriş yapılırken bir hata oluştu")
      }
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-background px-4">
      <Card className="w-full max-w-md">
        <CardHeader className="text-center">
          <div className="flex justify-center mb-2">
            <span className="text-2xl font-bold text-primary">OnlineMenu</span>
          </div>
          <CardTitle className="text-xl">Yönetim Paneli</CardTitle>
          <CardDescription>
            Hesabınıza giriş yapın
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <Button
            type="button"
            className="w-full"
            onClick={() => void handleKeycloakLogin()}
          >
            Keycloak ile giriş
          </Button>

          {DEV_LOGIN_ENABLED && (
            <>
              <div className="relative">
                <div className="absolute inset-0 flex items-center">
                  <span className="w-full border-t" />
                </div>
                <div className="relative flex justify-center text-xs uppercase">
                  <span className="bg-card px-2 text-muted-foreground">veya (dev)</span>
                </div>
              </div>
              <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="email">E-posta</Label>
                  <Input
                    id="email"
                    type="email"
                    placeholder="ornek@isletme.com"
                    {...register("email")}
                    aria-invalid={!!errors.email}
                  />
                  {errors.email && (
                    <p className="text-sm text-destructive">{errors.email.message}</p>
                  )}
                </div>

                <div className="space-y-2">
                  <Label htmlFor="password">Şifre</Label>
                  <Input
                    id="password"
                    type="password"
                    placeholder="••••••••"
                    {...register("password")}
                    aria-invalid={!!errors.password}
                  />
                  {errors.password && (
                    <p className="text-sm text-destructive">
                      {errors.password.message}
                    </p>
                  )}
                </div>

                <Button type="submit" className="w-full" disabled={isSubmitting}>
                  {isSubmitting ? "Giriş yapılıyor..." : "Giriş Yap (dev)"}
                </Button>
              </form>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
